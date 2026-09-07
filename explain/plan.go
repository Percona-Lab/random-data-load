// Package explain reads a postgres text-format EXPLAIN and works out what the
// data behind it must look like.
//
// Reproducing a plan means matching the numbers the planner read, and those
// numbers are in the plan itself: a sequential scan's cost says how many pages
// the table takes, a filter's removed-row count says how selective a predicate
// is, an aggregate's estimate says how many distinct values a column holds.
// Working them out by hand is arithmetic that is the same every time, so it is
// done here instead.
package explain

import (
	"regexp"
	"strconv"
	"strings"
)

// Node is one line of the plan, with the lines describing it that follow.
type Node struct {
	Name     string // "Seq Scan", "Parallel Bitmap Heap Scan", "Hash Join"
	Relation string // the table it reads, when it reads one
	Alias    string // the name the query gave that table
	Index    string // the index it reads, when it reads one
	Indent   int

	StartCost, TotalCost float64
	EstRows              int64
	Width                int64

	ActRows  int64
	Loops    int64
	Executed bool // false for a node the run never reached

	Details []string
	Parent  *Node
}

// Plan is a parsed EXPLAIN, in the order the nodes were printed.
type Plan struct {
	Nodes []*Node

	// aliases maps the name the query used to the table it stands for, so a
	// condition written on an alias can be attributed to a real table.
	aliases map[string]string

	// groupProducts holds what a multi-column GROUP BY says, which is a
	// product of distinct counts rather than any one of them.
	groupProducts []string
}

var nodeRe = regexp.MustCompile(
	`^(\s*)(?:->\s+)?([A-Z][^(]*?)\s+\(cost=([\d.]+)\.\.([\d.]+) rows=(\d+) width=(\d+)\)` +
		`(?:\s+\((?:actual time=[\d.]+\.\.[\d.]+ rows=(\d+) loops=(\d+)|never executed)\))?`)

// Parse reads the text postgres prints for EXPLAIN, with or without ANALYZE.
func Parse(text string) *Plan {
	plan := &Plan{aliases: map[string]string{}}

	var current *Node
	for _, line := range strings.Split(text, "\n") {
		m := nodeRe.FindStringSubmatch(line)
		if m == nil {
			// Anything indented under a node describes it: Filter, Index Cond,
			// Group Key, Rows Removed by Filter, Heap Blocks, Workers.
			if current != nil && indentOf(line) > current.Indent && strings.TrimSpace(line) != "" {
				current.Details = append(current.Details, strings.TrimSpace(line))
			}
			continue
		}

		node := &Node{
			Indent:    len(m[1]),
			StartCost: parseFloat(m[3]),
			TotalCost: parseFloat(m[4]),
			EstRows:   parseInt(m[5]),
			Width:     parseInt(m[6]),
			Loops:     1,
		}
		node.Name, node.Relation, node.Alias, node.Index = parseDescriptor(strings.TrimSpace(m[2]))
		if m[7] != "" {
			node.Executed = true
			node.ActRows = parseInt(m[7])
			node.Loops = parseInt(m[8])
		}

		for i := len(plan.Nodes) - 1; i >= 0; i-- {
			if plan.Nodes[i].Indent < node.Indent {
				node.Parent = plan.Nodes[i]
				break
			}
		}

		if node.Relation != "" {
			plan.aliases[node.Alias] = node.Relation
			plan.aliases[node.Relation] = node.Relation
		}
		plan.Nodes = append(plan.Nodes, node)
		current = node
	}
	return plan
}

// parseDescriptor splits what postgres writes before the cost into the node's
// name and what it reads.
//
// A bitmap index scan names its index after "on" where every other node names
// its table, which is the one case worth special-casing: read as a table it
// would invent one that does not exist.
func parseDescriptor(desc string) (name, relation, alias, index string) {
	if i := strings.Index(desc, " using "); i >= 0 {
		name = desc[:i]
		rest := desc[i+len(" using "):]
		if j := strings.Index(rest, " on "); j >= 0 {
			index = rest[:j]
			relation, alias = splitRelationAlias(rest[j+len(" on "):])
			return
		}
		return name, "", "", rest
	}

	if i := strings.Index(desc, " on "); i >= 0 {
		name = desc[:i]
		target := desc[i+len(" on "):]
		if strings.HasSuffix(name, "Bitmap Index Scan") {
			return name, "", "", target
		}
		relation, alias = splitRelationAlias(target)
		return
	}

	return desc, "", "", ""
}

func splitRelationAlias(s string) (relation, alias string) {
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return "", ""
	}
	relation = unqualify(parts[0])
	alias = relation
	if len(parts) > 1 {
		alias = parts[1]
	}
	return relation, alias
}

// unqualify drops a schema prefix, so public.orders and orders are one table.
func unqualify(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

// Detail returns the value of the first detail line with this prefix.
func (n *Node) Detail(prefix string) (string, bool) {
	for _, d := range n.Details {
		if strings.HasPrefix(d, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(d, prefix)), true
		}
	}
	return "", false
}

// DetailInt reads a detail line holding a single number.
func (n *Node) DetailInt(prefix string) (int64, bool) {
	raw, ok := n.Detail(prefix)
	if !ok {
		return 0, false
	}
	digits := regexp.MustCompile(`\d+`).FindString(raw)
	if digits == "" {
		return 0, false
	}
	return parseInt(digits), true
}

// IsParallel reports whether the workers each read a share of the node rather
// than all of it, which decides whether its counts have to be multiplied by
// the loop count to reach a total.
func (n *Node) IsParallel() bool {
	return strings.HasPrefix(n.Name, "Parallel")
}

// Scans reports whether this node reads a table.
func (n *Node) Scans() bool {
	return n.Relation != "" && strings.Contains(n.Name, "Scan")
}

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func parseInt(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
