package explain

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// The planner's default cost settings. A plan built with different ones puts
// the page count out by the same factor, which is why the page figures say
// which settings they assume.
const (
	seqPageCost     = 1.0
	cpuTupleCost    = 0.01
	cpuOperatorCost = 0.0025
)

// minFilterSample is how many rows a filter has to have seen before the share
// it kept is worth reading as a selectivity. A point lookup that removed 0 of
// 1 row says nothing about the column, and reading it as 100% would be worse
// than saying nothing.
const minFilterSample = 100

// TableStat is what the plan says about one table.
type TableStat struct {
	Table  string
	Rows   int64
	Pages  int64
	Width  int64
	Source string // how Rows was arrived at
	Note   string // why a figure is missing or approximate
}

// Selectivity is the share of a table's rows a predicate keeps.
type Selectivity struct {
	Table, Column, Value string
	Fraction             float64
	Rows                 int64 // matching rows, when the plan counted them
	Source               string
}

// Distinct is how many different values a column holds.
type Distinct struct {
	Table, Column string
	Count         int64
	Source        string
}

// FanOut is how many child rows each parent row has.
type FanOut struct {
	Child  string
	Rows   float64
	Source string
}

// Stats is everything the plan gives up.
type Stats struct {
	Tables        []TableStat
	Selectivities []Selectivity
	Distincts     []Distinct
	FanOuts       []FanOut
	Warnings      []string
}

// Derive works out what the data behind this plan looks like.
//
// known carries row counts the plan cannot supply itself, which is common: a
// table reached only through an index scan never reveals its size, and the
// share a predicate keeps cannot be turned into a fraction without it. Pass
// what the reported side said about its tables and the rest follows.
func (p *Plan) Derive(known map[string]int64) *Stats {
	stats := &Stats{}
	rows := p.tableRows(known, stats)
	p.tablePages(rows)

	for _, table := range sortedKeys(rows) {
		stats.Tables = append(stats.Tables, *rows[table])
	}
	stats.Selectivities = p.selectivities(rows)
	stats.Distincts = p.distincts()
	stats.FanOuts = p.fanOuts()
	stats.Warnings = append(stats.Warnings, p.groupProducts...)
	return stats
}

// tableRows settles how many rows each table holds.
//
// A sequential scan is the only node that reveals it directly: it reads every
// row, so what it kept plus what its filter removed is the table. Under
// parallel workers each loop reads a share, so those have to be added up.
func (p *Plan) tableRows(known map[string]int64, stats *Stats) map[string]*TableStat {
	rows := map[string]*TableStat{}

	for table, count := range known {
		rows[unqualify(table)] = &TableStat{Table: unqualify(table), Rows: count, Source: "given"}
	}

	for _, node := range p.Nodes {
		if !node.Scans() {
			continue
		}
		stat, ok := rows[node.Relation]
		if !ok {
			stat = &TableStat{Table: node.Relation}
			rows[node.Relation] = stat
		}
		if stat.Width == 0 {
			stat.Width = node.Width
		}
		if stat.Rows != 0 {
			continue
		}

		if !strings.HasSuffix(node.Name, "Seq Scan") {
			stat.Note = "reached only through an index, which never shows the table's size"
			continue
		}
		if !node.Executed {
			if _, filtered := node.Detail("Filter:"); !filtered {
				stat.Rows, stat.Source = node.EstRows, "sequential scan estimate (no ANALYZE in this plan)"
			}
			continue
		}

		removed, _ := node.DetailInt("Rows Removed by Filter:")
		scanned := node.ActRows + removed
		if node.IsParallel() {
			// every worker read its own share of the table
			stat.Rows = scanned * node.Loops
			stat.Source = "parallel sequential scan rows x loops"
			stat.Note = "workers share the table unevenly, so this is approximate"
			continue
		}
		stat.Rows = scanned
		stat.Source = "sequential scan actual rows + rows removed by filter"
	}

	for _, table := range sortedKeys(rows) {
		if rows[table].Rows == 0 && rows[table].Note == "" {
			rows[table].Note = "not derivable from this plan"
		}
	}
	if len(known) == 0 {
		stats.Warnings = append(stats.Warnings,
			"no row counts were given, so any table read only through an index has no size and its predicates have no selectivity. Pass what the reported side said with --rows")
	}
	return rows
}

// tablePages inverts a sequential scan's cost into a page count.
//
// The cost is relpages*seq_page_cost + reltuples*(cpu_tuple_cost +
// cpu_operator_cost per qual), and every term but relpages is known. Page
// count decides scan costs, and scan costs decide the plan, so this is usually
// the figure that has to be hit to reproduce one.
func (p *Plan) tablePages(rows map[string]*TableStat) {
	for _, node := range p.Nodes {
		stat, ok := rows[node.Relation]
		if !ok || stat.Pages != 0 {
			continue
		}

		if node.Name == "Seq Scan" && stat.Rows > 0 {
			quals := 0
			if filter, found := node.Detail("Filter:"); found {
				quals = countQuals(filter)
			}
			cpu := float64(stat.Rows) * (cpuTupleCost + cpuOperatorCost*float64(quals))
			if pages := (node.TotalCost - cpu) / seqPageCost; pages > 0 {
				stat.Pages = int64(math.Round(pages))
			}
			continue
		}
		if blocks, found := node.DetailInt("Heap Blocks: exact="); found && blocks > 0 {
			stat.Pages = blocks
			if stat.Note == "" {
				stat.Note = "pages are the bitmap scan's heap blocks, a lower bound on the table"
			}
		}
	}
}

var (
	equalityRe = regexp.MustCompile(`(?:\(*)(?:([a-z_][a-z0-9_$]*)\.)?\(?([a-z_][a-z0-9_$]*)\)?(?:::[a-z0-9_ ]+)?\s*=\s*(?:'([^']*)'|(-?\d+(?:\.\d+)?))`)
	notBoolRe  = regexp.MustCompile(`\(NOT (?:([a-z_][a-z0-9_$]*)\.)?([a-z_][a-z0-9_$]*)\)`)
	bareBoolRe = regexp.MustCompile(`^\(?(?:([a-z_][a-z0-9_$]*)\.)?([a-z_][a-z0-9_$]*)\)?$`)
)

// selectivities reads how much of a table each predicate keeps.
//
// The same predicate is usually written on more than one node, and the nodes
// do not agree: a bitmap index scan counts every row the value matches, while
// the heap scan above it rechecks that condition on rows a second filter has
// already thinned out. Reading the first node that mentions a predicate
// therefore gets it wrong, so every mention is scored and the most direct one
// kept.
func (p *Plan) selectivities(rows map[string]*TableStat) []Selectivity {
	best := map[string]Selectivity{}
	rank := map[string]int{}

	for _, node := range p.Nodes {
		for _, prefix := range []string{"Filter:", "Index Cond:", "Recheck Cond:"} {
			cond, ok := node.Detail(prefix)
			if !ok {
				continue
			}
			preds := parsePredicates(cond)
			for _, pred := range preds {
				table := p.tableFor(node, pred.qualifier)
				if table == "" {
					continue
				}

				sel, matching, source, quality := node.selectivityOf(prefix, len(preds), rows[table])
				if source == "" {
					continue
				}
				key := table + "." + pred.column + "=" + pred.value
				if existing, seen := rank[key]; seen && existing >= quality {
					continue
				}
				rank[key] = quality
				best[key] = Selectivity{
					Table: table, Column: pred.column, Value: pred.value,
					Fraction: sel, Rows: matching, Source: source,
				}
			}
		}
	}

	found := make([]Selectivity, 0, len(best))
	for _, key := range sortedKeys(best) {
		found = append(found, best[key])
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].Table != found[j].Table {
			return found[i].Table < found[j].Table
		}
		return found[i].Column < found[j].Column
	})
	return found
}

// selectivityOf works out the share this node's condition kept, and how much
// the answer is worth.
//
// A filter counting the rows it threw away settles a single predicate outright.
// It settles nothing about a predicate written alongside others, because the
// count it kept is what all of them together kept. An index condition never
// sees the rows it excluded, so it only yields a fraction against a known table
// size -- and a recheck condition sits above whatever else the heap scan
// filters, so its rows are the fewest of all and the least trustworthy.
func (n *Node) selectivityOf(prefix string, predicates int, table *TableStat) (fraction float64, matching int64, source string, quality int) {
	if prefix == "Filter:" && n.Executed {
		removed, ok := n.DetailInt("Rows Removed by Filter:")
		switch {
		case ok && predicates > 1:
			return 0, 0, "", 0 // the ratio belongs to the whole filter, not to one of its parts
		case ok && n.ActRows+removed >= minFilterSample:
			kept, seen := n.ActRows, n.ActRows+removed
			if n.IsParallel() {
				kept, seen = kept*n.Loops, seen*n.Loops
			}
			return float64(kept) / float64(seen), kept, "rows kept vs rows removed by filter", 3
		case ok:
			return 0, 0, "", 0 // too few rows seen to mean anything
		}
	}

	if table == nil || table.Rows <= 0 {
		return 0, 0, "", 0
	}

	quality = 1
	switch {
	case prefix == "Recheck Cond:":
		// the heap scan may also carry a Filter, so these rows are thinner
		quality = 0
	case strings.HasSuffix(n.Name, "Bitmap Index Scan"):
		// counts every row matching the value, before anything else applies
		quality = 2
	}

	if n.Executed {
		matching = n.ActRows
		if n.IsParallel() {
			matching *= n.Loops
		}
		return float64(matching) / float64(table.Rows), matching, "matching rows / table rows", quality
	}
	return float64(n.EstRows) / float64(table.Rows), n.EstRows, "estimated rows / table rows (no ANALYZE in this plan)", quality
}

// distincts reads how many values a grouped column holds.
//
// The planner sizes an aggregate from n_distinct, so its row estimate is that
// figure back again whenever a single column is grouped.
func (p *Plan) distincts() []Distinct {
	found := []Distinct{}
	seen := map[string]bool{}

	for _, node := range p.Nodes {
		if !strings.Contains(node.Name, "Aggregate") || strings.HasPrefix(node.Name, "Partial") {
			continue
		}
		key, ok := node.Detail("Group Key:")
		if !ok {
			continue
		}
		columns := strings.Split(key, ", ")
		if len(columns) != 1 {
			// The estimate is the product of each column's distinct count, so
			// it cannot be split -- but it does bound them, and a plan
			// grouping on two columns is where that product has to be matched.
			p.groupProducts = append(p.groupProducts, fmt.Sprintf(
				"%s estimates %d groups over (%s): the distinct counts of those columns have to multiply out to about that",
				node.Name, node.EstRows, key))
			continue
		}
		m := bareBoolRe.FindStringSubmatch(strings.TrimSpace(columns[0]))
		if m == nil {
			continue
		}
		table := p.tableFor(node, m[1])
		if table == "" || seen[table+"."+m[2]] {
			continue
		}
		seen[table+"."+m[2]] = true
		found = append(found, Distinct{
			Table: table, Column: m[2], Count: node.EstRows,
			Source: node.Name + " group estimate",
		})
	}
	return found
}

// fanOuts reads how many child rows each parent row has, which is what a
// relationship's sampling has to reproduce.
func (p *Plan) fanOuts() []FanOut {
	found := []FanOut{}
	seen := map[string]bool{}

	for _, node := range p.Nodes {
		if !node.Scans() || !node.Executed || node.Loops <= 1 || node.IsParallel() {
			continue
		}
		if node.Parent == nil || !strings.Contains(node.Parent.Name, "Nested Loop") {
			continue
		}
		if seen[node.Relation] {
			continue
		}
		seen[node.Relation] = true
		found = append(found, FanOut{
			Child:  node.Relation,
			Rows:   float64(node.ActRows),
			Source: fmt.Sprintf("%d rows over %d loops of the nested loop", node.ActRows, node.Loops),
		})
	}
	return found
}

// tableFor resolves the table a condition is written against: the alias it
// names, or the table the node itself reads.
func (p *Plan) tableFor(node *Node, qualifier string) string {
	if qualifier != "" {
		if table, ok := p.aliases[qualifier]; ok {
			return table
		}
		return ""
	}
	for n := node; n != nil; n = n.Parent {
		if n.Relation != "" {
			return n.Relation
		}
	}
	return ""
}

type predicate struct{ qualifier, column, value string }

// parsePredicates pulls the column-equals-literal pairs out of a condition.
func parsePredicates(cond string) []predicate {
	preds := []predicate{}

	for _, m := range equalityRe.FindAllStringSubmatch(cond, -1) {
		value := m[3]
		if value == "" {
			value = m[4]
		}
		if value == "" || isNoise(m[2]) {
			continue
		}
		preds = append(preds, predicate{qualifier: m[1], column: m[2], value: value})
	}

	for _, m := range notBoolRe.FindAllStringSubmatch(cond, -1) {
		preds = append(preds, predicate{qualifier: m[1], column: m[2], value: "false"})
	}
	if m := bareBoolRe.FindStringSubmatch(strings.TrimSpace(cond)); m != nil && !isNoise(m[2]) {
		preds = append(preds, predicate{qualifier: m[1], column: m[2], value: "true"})
	}
	return preds
}

// isNoise drops the words postgres writes inside a condition that are not
// column names.
func isNoise(word string) bool {
	switch word {
	case "text", "numeric", "date", "timestamp", "timestamptz", "bpchar", "varchar", "int", "int2", "int4", "int8", "bool", "boolean", "any", "not":
		return true
	}
	return false
}

// countQuals counts the conditions a filter applies, which is what the cost
// charges cpu_operator_cost per row for.
func countQuals(filter string) int {
	return strings.Count(filter, " AND ") + strings.Count(filter, " OR ") + 1
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
