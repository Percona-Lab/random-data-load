package cmd

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/Percona-Lab/random-data-load/db"
	"github.com/Percona-Lab/random-data-load/explain"
	"github.com/Percona-Lab/random-data-load/frequency"
	"github.com/Percona-Lab/random-data-load/generate"
	"github.com/Percona-Lab/random-data-load/query"
	"github.com/pkg/errors"
)

// VerifyCmd reads a filled database back and holds it against what was asked
// for.
//
// Every run ends the same way: count the rows of each table, work out what
// share of them each predicate keeps, count the distinct values, read back the
// page and row counts the planner will see, and compare each of those against
// the reported figures. That is mechanical, it is the same SQL every time, and
// getting one of the comparisons subtly wrong is not visible afterwards. It is
// also the table that belongs in a support ticket, which is reason enough for
// the tool to print it rather than the caller to assemble it.
//
// It takes the same inputs the run took -- the target EXPLAIN, --stat-file,
// --rows -- so the expectations are the ones the run was given, not
// a second set written by hand.
type VerifyCmd struct {
	DB db.Config `embed:""`

	File string `arg:"" optional:"" type:"path" help:"File holding the EXPLAIN of the reported side, the same one \"explain-stat\" reads. Its row counts, page counts, selectivities and distinct counts are what the generated tables are held against."`

	Query    string               `help:"The query being reproduced. Its tables are the ones read back."`
	Table    string               `help:"Table to read back. With --query, it restricts the check to that single table."`
	Rows     generate.PerTableInt `name:"rows" help:"Row counts the tables were filled with, exactly as they were given to \"run\": --rows=\"1000;orders=500000\". Also fills in the sizes a plan cannot reveal on its own." default:""`
	StatFile string               `name:"stat-file" help:"The statistics export the run was given. Its null_frac, most_common_freqs and avg_width are compared against what the generated table holds." type:"path"`

	MaxCommonVals int     `name:"max-common-vals" help:"Check only the first N most common values of each column from --stat-file. 0 checks them all." default:"5"`
	Tolerance     float64 `name:"tolerance" help:"How far a generated figure may sit from the reported one before it is called out, as a fraction. 0.05 is 5%." default:"0.05"`
	Analyze       bool    `name:"analyze" negatable:"" help:"Collect statistics before reading the page and row counts back. A table filled a moment ago carries none, so the catalog would report nothing at all." default:"true"`
	Strict        bool    `name:"strict" help:"Exit non-zero when a figure sits outside --tolerance, so a script can stop on it."`
	All           bool    `name:"all" help:"Print every figure. By default only the ones outside --tolerance or not measured are, and the rest are counted."`
}

func (cmd *VerifyCmd) Run() error {
	if _, err := db.Connect(cmd.DB); err != nil {
		return err
	}

	plan, err := cmd.reportedPlan()
	if err != nil {
		return err
	}
	stats, err := cmd.reportedColumns()
	if err != nil {
		return err
	}

	targets, err := cmd.targets(plan, stats)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return errors.New("no table to read back. Name them with --query or --table, or pass the EXPLAIN that names them")
	}

	report := &verifyReport{tolerance: cmd.Tolerance, all: cmd.All}
	for _, target := range targets {
		cmd.check(target, report)
	}

	fmt.Print(report.render())
	if cmd.Strict && report.off > 0 {
		return errors.New(report.summary())
	}
	return nil
}

// reportedPlan reads the target EXPLAIN, which is optional: a run tuned only
// with --rows and a statistics export has no plan to hold against.
func (cmd *VerifyCmd) reportedPlan() (*explain.Stats, error) {
	if cmd.File == "" {
		return &explain.Stats{}, nil
	}
	text, err := os.ReadFile(cmd.File)
	if err != nil {
		return nil, errors.Wrapf(err, "reading the plan from %s", cmd.File)
	}
	parsed := explain.Parse(string(text))
	if len(parsed.Nodes) == 0 {
		return nil, errors.Errorf("no plan node found in %s. This reads the text EXPLAIN prints, the default format, with or without ANALYZE", cmd.File)
	}
	return parsed.Derive(cmd.Rows.Named()), nil
}

func (cmd *VerifyCmd) reportedColumns() ([]frequency.ColumnStats, error) {
	if cmd.StatFile == "" {
		return nil, nil
	}
	return frequency.LoadStats(cmd.StatFile)
}

// verifyTarget is one table, everything the reported side said about it, and
// everything that has to be read back from it.
type verifyTarget struct {
	table   *db.Table
	measure db.TableMeasure

	rows, pages, width int64
	rowsSource         string

	// widthSource says which side gave the row width, because that decides
	// whether missing it is a failure: see the comparison in check.
	widthSource string

	selectivities []explain.Selectivity
	distincts     []explain.Distinct
	columns       []frequency.ColumnStats

	unreadable string
	skipped    []string
}

// targets works out which tables to read back and what to ask each of them.
//
// The table set is the union of everything naming one: --table, the query's
// own tables, the relations the plan scans and the tables the statistics
// export covers. A column the local table does not have is dropped with a note
// rather than turned into SQL that cannot run.
func (cmd *VerifyCmd) targets(plan *explain.Stats, stats []frequency.ColumnStats) ([]*verifyTarget, error) {
	names, err := cmd.tableNames(plan, stats)
	if err != nil {
		return nil, err
	}

	targets := []*verifyTarget{}
	byName := map[string]*verifyTarget{}
	for _, name := range names {
		target := &verifyTarget{}
		table, err := db.LoadTableColumns(cmd.DB.Database, name)
		if err != nil {
			target.table = &db.Table{Name: name}
			target.unreadable = err.Error()
		} else {
			target.table = table
			// which columns are keys decides what of a dump a column can be
			// held against: see the value frequencies in check
			constraints, err := db.GetConstraints(table.Schema, table.Name)
			if err != nil {
				target.skipped = append(target.skipped, fmt.Sprintf("%s: reading its foreign keys: %v. Their values are compared as any other column's", table.Name, err))
			}
			table.Constraints = constraints
		}
		targets = append(targets, target)
		byName[strings.ToLower(target.table.Name)] = target
	}

	for _, stat := range plan.Tables {
		if target, ok := byName[strings.ToLower(stat.Table)]; ok {
			target.rows, target.pages, target.width = stat.Rows, stat.Pages, stat.Width
			target.rowsSource = stat.Source
		}
	}
	for _, sel := range plan.Selectivities {
		target, ok := byName[strings.ToLower(sel.Table)]
		if !ok {
			continue
		}
		if target.want(sel.Column) {
			target.selectivities = append(target.selectivities, sel)
			target.predicate(sel.Column, sel.Value)
		}
	}
	for _, distinct := range plan.Distincts {
		target, ok := byName[strings.ToLower(distinct.Table)]
		if !ok || !target.want(distinct.Column) {
			continue
		}
		target.distincts = append(target.distincts, distinct)
		target.measure.Distincts = append(target.measure.Distincts, distinct.Column)
	}
	for _, stat := range stats {
		target, ok := byName[strings.ToLower(stat.Tablename)]
		if !ok || !target.want(stat.Attname) {
			continue
		}
		target.columns = append(target.columns, stat)
		target.measure.Nulls = append(target.measure.Nulls, stat.Attname)
		for _, value := range cmd.commonValues(stat) {
			target.predicate(stat.Attname, value)
		}
	}

	// the row width the export describes, which is the sharper of the two: a
	// plan's "width=" counts the columns its scan outputs, and the export
	// counts the row
	for _, target := range targets {
		width, why := target.widthFromStats()
		switch {
		case width > 0:
			target.width, target.widthSource = width, widthSourceExport
		case why != "" && target.width == 0:
			target.skipped = append(target.skipped, why)
		}
	}

	// the row count every other figure is a share of, and the only one a run
	// given nothing but --rows can still be held against
	for _, target := range targets {
		if target.rows == 0 {
			if rows := cmd.Rows.For(target.table.Name); rows > 0 {
				target.rows, target.rowsSource = rows, "--rows"
			}
		}
		target.measure.Schema, target.measure.Table = target.table.Schema, target.table.Name
	}
	return targets, nil
}

// commonValues is the head of a column's most common values, the ones worth
// checking one by one. A dump holds up to default_statistics_target of them
// per column, and the tail is where a fraction is too small to measure
// against anyway.
func (cmd *VerifyCmd) commonValues(stat frequency.ColumnStats) []string {
	count := min(len(stat.MostCommonVals), len(stat.MostCommonFreqs))
	if cmd.MaxCommonVals > 0 && count > cmd.MaxCommonVals {
		count = cmd.MaxCommonVals
	}
	return stat.MostCommonVals[:count]
}

// want reports whether the table really holds this column, and remembers the
// ones it does not so the report can say what it left out.
func (t *verifyTarget) want(column string) bool {
	if t.unreadable != "" {
		return false
	}
	if t.table.FieldByName(column) != nil {
		return true
	}
	note := t.table.Name + "." + column + " is not a column of the generated table"
	if !contains(t.skipped, note) {
		t.skipped = append(t.skipped, note)
	}
	return false
}

// widthSourceExport marks a row width that came from the statistics export
// rather than from the plan.
const widthSourceExport = "--stat-file"

// widthFromStats is the row width the export describes: every column's
// avg_width added up.
//
// That sum is the figure the catalog hands back for the generated table --
// postgres' own "width of a row" is the same addition over the same columns --
// so unlike the plan's "width=" the two sides here are measuring one thing,
// and the run was aimed at it. It is the null fraction that is not in it:
// avg_width is measured over the rows holding a value, so comparing it to
// another avg_width needs no correction, while aiming a generator at a row
// width does. Both come out right when the run reproduces the column widths it
// was given.
//
// The whole table or nothing, for the reason "run" leaves a partial dump
// alone: the columns of an export narrowed by a --query add up to less than a
// row, and reporting that as the width to hit would call every correct table
// too wide. The second return value is why there is no width, for a report
// that would otherwise only say "no target".
func (t *verifyTarget) widthFromStats() (int64, string) {
	if t.unreadable != "" || len(t.columns) == 0 {
		return 0, ""
	}

	covered := map[string]struct{}{}
	var width, measured int64
	for _, cs := range t.columns {
		covered[strings.ToLower(cs.Attname)] = struct{}{}
		width += cs.AvgWidth
		if cs.AvgWidth > 0 {
			measured++
		}
	}
	if measured == 0 {
		return 0, ""
	}

	missing := []string{}
	for _, field := range t.table.Fields {
		if _, ok := covered[strings.ToLower(field.ColumnName)]; !ok {
			missing = append(missing, field.ColumnName)
		}
	}
	if len(missing) > 0 {
		return 0, fmt.Sprintf("%s: the export does not cover %s, so its avg_width adds up to less than a row and is not held against the generated width",
			t.table.Name, strings.Join(missing, ", "))
	}
	return width, ""
}

// predicate adds a "column = value" to read back, once however many figures
// need it: a value the plan filters on is often one the export measured too.
func (t *verifyTarget) predicate(column, value string) {
	wanted := db.Predicate{Column: column, Value: value}
	for _, existing := range t.measure.Predicates {
		if existing.Key() == wanted.Key() {
			return
		}
	}
	t.measure.Predicates = append(t.measure.Predicates, wanted)
}

func (cmd *VerifyCmd) tableNames(plan *explain.Stats, stats []frequency.ColumnStats) ([]string, error) {
	names := map[string]struct{}{}

	if cmd.Query != "" {
		parsed, _, _, _, err := query.ParseQuery(cmd.Query, cmd.DB.Engine, true)
		if err != nil {
			return nil, err
		}
		for name := range parsed {
			names[name] = struct{}{}
		}
	}
	for _, stat := range plan.Tables {
		names[stat.Table] = struct{}{}
	}
	for _, stat := range stats {
		names[stat.Tablename] = struct{}{}
	}
	// naming a table's expected row count is naming a table to look at
	for _, name := range cmd.Rows.Tables() {
		names[name] = struct{}{}
	}
	// --table narrows rather than adds, the same way it does on "run"
	if cmd.Table != "" {
		names = map[string]struct{}{cmd.Table: struct{}{}}
	}

	unique := make([]string, 0, len(names))
	for name := range names {
		unique = append(unique, name)
	}
	sort.Strings(unique)
	return unique, nil
}

// check reads one table back and records every comparison it can make.
func (cmd *VerifyCmd) check(target *verifyTarget, report *verifyReport) {
	name := target.table.Name
	if target.table.Schema != "" {
		name = target.table.Schema + "." + target.table.Name
	}

	if target.unreadable != "" {
		report.note(name + ": " + target.unreadable)
		return
	}

	if cmd.Analyze {
		if err := db.Analyze(target.table.Schema, target.table.Name); err != nil {
			report.note(fmt.Sprintf("%s: could not collect statistics, the page and row counts below are whatever the catalog last held (%v)", name, err))
		}
	}

	measured := db.Measure(target.measure)
	for _, err := range measured.Errors {
		report.note(err)
	}
	for _, skipped := range target.skipped {
		report.note(skipped)
	}

	report.add(comparison{
		subject: name, figure: figureRows,
		reported: float64(target.rows), hasTarget: target.rows > 0,
		generated: float64(measured.Rows),
		measured:  true, source: target.rowsSource,
	})

	storage, err := db.TableStorage(target.table.Schema, target.table.Name)
	if err != nil {
		report.note(fmt.Sprintf("%s: %v", name, err))
	} else {
		report.add(comparison{
			subject: name, figure: figurePages,
			reported: float64(target.pages), hasTarget: target.pages > 0,
			generated: float64(storage.Pages),
			measured:  true, note: storage.Note,
		})
		// The plan's width is the width of the columns that node outputs,
		// not the width of a stored row, so the two only line up when the
		// scan projects the whole table. It is worth printing -- it is what a
		// row-width target is aimed at -- and not worth failing a run over.
		//
		// The export's is the same addition the catalog does, column for
		// column, so that one is held to like any other figure. Except on
		// InnoDB, which reports a sampled average row length with the record
		// header in it rather than a sum of column widths, and would fail on
		// the difference between the two figures rather than on the data.
		width := comparison{
			subject: name, figure: figureWidth,
			reported: float64(target.width), hasTarget: target.width > 0,
			generated: float64(storage.Width),
			measured:  true, advisory: true,
		}
		switch {
		case target.widthSource == widthSourceExport:
			width.advisory = cmd.DB.Engine != "pg"
			width.source = "--stat-file: the export's avg_width added up over the table's columns, which is the sum the catalog reports back"
			if width.advisory {
				width.source = "--stat-file: the export's avg_width added up over the table's columns. InnoDB reports a sampled average row length, record header included, so the two are close rather than the same figure"
			}
		case target.width > 0:
			width.source = "the plan counts only the columns its scan outputs, the catalog counts every column"
			width.unlike = true
		}
		report.add(width)
		// Not a comparison against the reported side at all: it holds the
		// counted rows against what the planner believes the table holds, so
		// a stale or refused ANALYZE shows up as a figure rather than as a
		// silently wrong page count. InnoDB samples its estimate, so it is
		// never exact there and is never worth failing on.
		if storage.Tuples > 0 && measured.Rows > 0 {
			report.add(comparison{
				subject: name, figure: figureTuples,
				reported: float64(measured.Rows), hasTarget: true,
				generated: float64(storage.Tuples),
				measured:  true, advisory: true,
				source: "counted rows vs what the planner believes the table holds",
			})
		}
	}

	if measured.Rows == 0 {
		report.note(name + " holds no row, so nothing below it can be a share of anything")
		return
	}
	total := float64(measured.Rows)

	for _, sel := range target.selectivities {
		matching, ok := measured.Matching[db.Predicate{Column: sel.Column, Value: sel.Value}.Key()]
		report.add(comparison{
			subject: name + "." + sel.Column + " = " + sel.Value, figure: figureSelectivity,
			reported: sel.Fraction, hasTarget: true,
			generated: float64(matching) / total,
			measured:  ok, source: sel.Source,
		})
	}

	for _, distinct := range target.distincts {
		count, ok := measured.Distinct[distinct.Column]
		report.add(comparison{
			subject: name + "." + distinct.Column, figure: figureDistinct,
			reported: float64(distinct.Count), hasTarget: distinct.Count > 0,
			generated: float64(count),
			measured:  ok, source: distinct.Source,
		})
	}

	for _, stat := range target.columns {
		nulls, ok := measured.Nulls[stat.Attname]
		report.add(comparison{
			subject: name + "." + stat.Attname, figure: figureNullFrac,
			reported: stat.NullFrac, hasTarget: true,
			generated: float64(nulls) / total,
			measured:  ok, source: "--stat-file",
		})
		key := isForeignKey(target.table, stat.Attname)
		for i, value := range cmd.commonValues(stat) {
			matching, ok := measured.Matching[db.Predicate{Column: stat.Attname, Value: value}.Key()]
			c := comparison{
				subject: name + "." + stat.Attname + " = " + value, figure: figureValueFreq,
				reported: stat.MostCommonFreqs[i], hasTarget: true,
				generated: float64(matching) / total,
				measured:  ok, source: "--stat-file",
			}
			// The values a dump holds for a key are the source database's
			// parent ids. run never inserts them, it reproduces how skewed
			// the key is instead, so the share of one given id is not a
			// figure the generated key was ever aimed at.
			if key {
				c.advisory, c.unlike = true, true
				c.source = "--stat-file: a foreign key holds this run's parent ids, not the source's"
			}
			report.add(c)
		}
	}
}

// isForeignKey is whether a column is part of one of the table's foreign keys,
// matched the way run matches a dump's column to a table's.
func isForeignKey(table *db.Table, column string) bool {
	field := table.FieldByName(column)
	return field != nil && table.IsFieldInAnyConstraints(*field)
}

func contains(list []string, value string) bool {
	for _, existing := range list {
		if existing == value {
			return true
		}
	}
	return false
}

func formatFraction(f float64) string {
	switch {
	case f >= 0.01:
		return strconv.FormatFloat(f, 'f', 4, 64)
	case f >= 0.0001:
		return strconv.FormatFloat(f, 'f', 6, 64)
	default:
		return strconv.FormatFloat(f, 'g', 3, 64)
	}
}
