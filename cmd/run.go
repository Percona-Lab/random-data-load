package cmd

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Percona-Lab/random-data-load/db"
	"github.com/Percona-Lab/random-data-load/frequency"
	"github.com/Percona-Lab/random-data-load/generate"
	"github.com/Percona-Lab/random-data-load/query"
	"github.com/apoorvam/goterminal"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

type RunCmd struct {
	DB db.Config `embed:""`

	Table            string               `help:"Table to insert to. When using --query, --table will be used to restrict the tables to insert to."`
	Rows             generate.PerTableInt `name:"rows" required:"true" placeholder:"N|table=N" help:"Number of rows to insert. One number for every table, or per table, or both: --rows=\"1000;orders=500000;order_items=1500000\""`
	BulkSize         int64                `name:"bulk-size" help:"Number of rows per insert statement" default:"1000"`
	DryRun           bool                 `name:"dry-run" help:"Print queries to the standard output instead of inserting them into the db"`
	Truncate         bool                 `name:"truncate" help:"Empty the tables this run fills before inserting into them. Without it a second run adds to what the first one left, which is rarely what tuning a run wants. Never touches a table this run does not fill: a foreign key pointing in from outside makes it refuse rather than cascade."`
	Quiet            bool                 `name:"quiet" help:"Do not print progress bar"`
	WorkersCount     int                  `name:"workers" help:"How many workers to spawn. Only the random generation and sampling are parallelized. Insert queries are executed one at a time" default:"3"`
	MaxTextSize      int64                `help:"Limit the maximum size of long text, varchar and blob fields." default:"65535"`
	UUIDVersion      int                  `name:"uuid-version" help:"UUID v4 or v7 for uuid datatypes" default:"4" enum:"4,7"`
	MinGeneratedTime time.Time            `help:"Generated timestamps will be after this date. Format is RFC3339. Will default to --max-generated-time - 1 year"`
	MaxGeneratedTime time.Time            `help:"Generated timestamps will be before this date. Format is RFC3339. Will default to now()"`
	Query            string               `help:"Providing a query will enable to automatically discover the schema, insert recursively into tables, enforce implicit joins."`

	generate.ForeignKeyLinks
	AddForeignKeys    query.VirtualJoins                      `name:"add-fk" help:"Add foreign keys, if they are not explicitely created in the table schema. It can complement the foreign keys guessed from the --query, or be used to manually define foreign keys when using --no-fk-guess too. Format: --add-fk=\"parent_table.col1[,col2...]=child_table.colx[,coly...][; additional fk ]\". Example: --add-fk=\"customers.id,created_at=purchases.customer_id,created_at;purchases.id=items.purchase_id\""`
	FillFKParents     bool                                    `name:"fill-fk-parents" help:"Add the tables a foreign key points at to this run when they hold no row, filling them with --rows. Without it, such a run is refused up front naming them, rather than failing part way through with some tables already loaded."`
	NoFKGuess         bool                                    `name:"no-fk-guess" help:"Do not try to guess foreign keys from the --query missing in the schema. When a query is provided, it will analyze the expected JOINs and try to respect dependencies even when foreign keys are not explicitely created in the database objects. This flag will make the tool stick to the constraints defined in the database only, unless you add foreign keys manually with --add-fk." `
	NoSkipFields      bool                                    `name:"no-skip-fields" help:"Disable field whitelist system. When using a --query, it will get the list of fields being used as a whitelist in order to generate the minimal sets of fields required, unless --no-skip-fields is being used or any * has been found."`
	NullFreq          generate.PerTableFloat                  `name:"null-freq" help:"How often a nullable column is NULL, as a fraction between 0 and 1. One number for every column, or per column, or both: --null-freq=\"0.1;items.tags=0.73;items.price=0\"" default:"0.1"`
	ValuesFreqMap     frequency.FrequencyIndexValuesParameter `name:"values-freq-map" help:"Inject arbitrary values at fixed frequencies. The format is \"--values-freq-map=t1.c1=val1:0.75,val2:0.23;t1.c2=10:0.99\" so that val1 will be on 75% of rows and val2 on 23% for column c1" default:""` // TODO we're not checking if the total freq is above 1
	QueryParamsFreq   float64                                 `name:"query-param-freq" help:"Insert the literals the --query compares a column to, on this fraction of the rows, so that the query returns something. = and IN operators are handled. It is a selectivity nobody measured, so it is off by default and overrides anything --stat-file says about those values when you do set it. The run names the predicates nothing will match." default:"0"`
	TargetBytesPerRow generate.PerTableFloat                  `name:"target-bytes-per-row" help:"Aim the rows of a table at an average width, in bytes, by writing longer or shorter values into its free-text columns. It is the figure a plan's \"width=\" is built from, and the one that decides how many rows fit in a page. Can be given per table: --target-bytes-per-row=\"97;order_items=24\"" default:""`
	TargetRelpages    generate.PerTableFloat                  `name:"target-relpages" help:"Aim a table at a page count instead, which --rows and postgres' page layout turn into a row width. Page count is what a sequential scan's cost is built from, so it is usually the figure a reproduction has to hit. Can be given per table: --target-relpages=\"orders=12345\". Postgres only. " default:""`

	StatFile string `name:"stat-file" help:"Scan a column statistics export and reuse its null_frac, most_common_vals and most_common_freqs as --null-freq and --values-freq-map. Use the \"export-stat\" subcommand to get the command producing that file." type:"path"`
}

// Run starts inserting data.
func (cmd *RunCmd) Run() error {

	// Quick check to confirm database connection
	_, err := db.Connect(cmd.DB)
	if err != nil {
		return err
	}

	if cmd.MaxGeneratedTime.IsZero() {
		cmd.MaxGeneratedTime = time.Now()
	}
	if cmd.MinGeneratedTime.IsZero() {
		cmd.MinGeneratedTime = cmd.MaxGeneratedTime.Add(-1 * time.Duration(24*365) * time.Hour)
	}

	// --coin-flip-percent, --normal-stddev and --normal-mean all describe the
	// parent table a relationship samples, not the table being filled, so they
	// are guarded and defaulted per relationship, in the samplers, which know
	// the parent's real row count. Guarded here against --rows, a small parent
	// with a large child never tripped the guard, which is the one case that
	// needed it.
	if cmd.DefaultRelationship == generate.ParetoFlag || len(cmd.Pareto) > 0 {
		// every value has to be in range, not just the one given for every
		// relationship: a per-parent override is used exactly the same way
		for _, s := range cmd.ParetoS.Values() {
			if s <= 1.0 {
				return errors.Errorf("--pareto-s needs to be >1, got %g", s)
			}
		}
		for _, v := range cmd.ParetoV.Values() {
			if v < 1 {
				return errors.Errorf("--pareto-v needs to be >=1, got %g", v)
			}
		}
	}

	if err := cmd.checkWidthTargets(); err != nil {
		return err
	}

	tablesNames := map[string]struct{}{}
	identifiers := map[string]struct{}{}
	joins := []query.VirtualJoin{}
	queryParams := map[string][]string{}

	if cmd.Query == "" && cmd.Table == "" {
		return errors.New("Need either a --query or a --table")
	}

	if cmd.Query != "" {
		tablesNames, identifiers, joins, queryParams, err = query.ParseQuery(cmd.Query, cmd.DB.Engine, cmd.NoFKGuess)
		if err != nil {
			return err
		}
		log.Debug().Interface("identifiers", identifiers).Interface("joins", joins).Interface("queryParams", queryParams).Msg("query parsed")
	}
	// if --table is given, we will restrict inserts to this table only
	// we will still skip some columns and potentially have virtual FKs
	if cmd.Table != "" {
		tablesNames = map[string]struct{}{cmd.Table: struct{}{}}
	}

	if err := cmd.applyNullFreq(); err != nil {
		return err
	}
	log.Debug().Interface("freq-map", frequency.SharedTableFrequency).Msg("frequency maps parsed")
	frequency.MergeQueryParameters(queryParams, cmd.QueryParamsFreq)
	log.Debug().Interface("freq-map", frequency.SharedTableFrequency).Msg("merged query params into frequency map")

	// loading base tables
	tables := []*db.Table{}
	for tableKey := range tablesNames {
		table, err := db.LoadTable(cmd.DB.Database, tableKey)
		if err != nil {
			return err
		}

		if cmd.Query != "" && !cmd.NoSkipFields {
			table.SkipBasedOnIdentifiers(identifiers)
		}

		tables = append(tables, table)
	}
	// we can autocomplete foreign keys
	joins = append(joins, cmd.AddForeignKeys...)
	if len(joins) > 0 {
		db.AddVirtualFKs(tables, joins)
	}

	// Every key of every table in the run has to have something to point at
	// before anything is written, or the run dies part way through with some
	// tables already loaded.
	tables, err = cmd.resolveForeignKeyParents(tables)
	if err != nil {
		return err
	}

	if cmd.StatFile != "" {
		// After the tables are loaded, so that an exported column can be
		// matched against the real one: the catalog reports folded names, and
		// both the frequency map and the generator are keyed on the names it
		// gave back. And after the foreign keys are settled, guessed ones
		// included, because what of the dump can be reused for a column
		// depends on whether that column is a key.
		if err := cmd.mergeStats(tables); err != nil {
			return err
		}
		log.Debug().Interface("freq-map", frequency.SharedTableFrequency).Msg("merged exported statistics into frequency map")
	}

	// now we have the full table list and every key it will have to satisfy,
	// we check for any loops. A guessed key can close one just as well as a
	// key of the schema, so this comes after they are added.
	for _, table := range tables {
		copiedTable, err := table.IdentifyAndResolveSelfReferencingConstraintLoop()
		if err != nil {
			return err
		}
		if copiedTable != nil {
			rows := cmd.Rows.For(table.Name)
			log.Info().Str("table", table.Name).Int64("rows", rows/2).Msg("table has a self-referencing foreign key. Setting --rows to half for this table since we will insert twice to it to resolve the dependency.")
			cmd.Rows.Set(table.Name, rows/2)
			tables = append([]*db.Table{copiedTable}, tables...)

		} else if table.HasAnyConstraintLoop() {
			return errors.Errorf("table %s has a foreign key loop", table.Name)
		}
	}

	reportPredicatesNothingWillMatch(tables, queryParams)

	// and identify which constraints should be "garanteed" for this run
	for _, table := range tables {
		table.FlagConstraintThatArePartsOfThisRun(tables)
	}
	// so that we can sort based on the dependencies we need to satisfy
	tablesSorted, err := db.SortTables(tables)
	if err != nil {
		return err
	}

	for _, table := range tablesSorted {
		log.Debug().Str("table", table.Name).Int("number of constraint", len(table.Constraints)).Msg("tables sorted")
	}

	if err := reportUnsupportedFields(tablesSorted); err != nil {
		return err
	}

	// Emptying comes after every check that can refuse the run, so a refusal
	// leaves the tables as they were.
	if cmd.Truncate && !cmd.DryRun {
		log.Info().Strs("tables", tableNames(tablesSorted)).Msg("emptying the tables this run fills, as --truncate asks")
		if err := db.TruncateTables(tablesSorted); err != nil {
			return errors.Wrap(err, "--truncate")
		}
	}

	// one at a time.
	// Parallelizing here will complexify the foreign links, for probably not so much gain
	for _, table := range tablesSorted {
		err = cmd.run(table)
		if err != nil {
			// if FK fails on mysql, it could be due to an extra foreign keys even though the referenced table do not exist
			if cmd.DB.Engine == "mysql" && strings.Contains(err.Error(), "Error 1452") {
				helperForMySQLFKChecks(tablesSorted, err)
			}
			return errors.Wrapf(err, "failed to insert on %s.%s", table.Schema, table.Name)
		}
	}

	return err
}

// checkWidthTargets refuses a row width target that cannot mean anything,
// before a single row is written.
func (cmd *RunCmd) checkWidthTargets() error {
	if len(cmd.TargetRelpages) > 0 && cmd.DB.Engine != "pg" {
		return errors.New("--target-relpages is postgres geometry: a page holds its header, a line pointer per row and a tuple header per row, and what is left is the room the columns have. InnoDB organises a table by its primary key and reports a size it sampled rather than counted, so the same arithmetic would not mean anything. Use --target-bytes-per-row")
	}

	for table := range cmd.TargetRelpages {
		if _, both := cmd.TargetBytesPerRow[table]; !both {
			continue
		}
		named := "every table"
		if table != "" {
			named = table
		}
		return errors.Errorf("--target-bytes-per-row and --target-relpages both given for %s, and they are two ways of asking for the same thing. Keep one", named)
	}
	return nil
}

// rowWidthTarget is the width this table's rows are aimed at, in bytes.
//
// --target-relpages is the figure a plan actually turns on, and it is the same
// target seen from the other side: the row count this run was given says how
// many rows have to fit in those pages, and postgres' page layout says how
// wide a row that makes.
func (cmd *RunCmd) rowWidthTarget(table *db.Table, rows int64) int64 {
	if cmd.TargetBytesPerRow.IsSetFor(table.Name) {
		return int64(math.Round(cmd.TargetBytesPerRow.For(table.Name)))
	}
	if !cmd.TargetRelpages.IsSetFor(table.Name) {
		return 0
	}

	pages := int64(math.Round(cmd.TargetRelpages.For(table.Name)))
	bytes, reached := generate.BytesPerRowForPages(rows, pages)
	if bytes <= 0 {
		log.Warn().Str("table", table.Name).Int64("pages", pages).Int64("rows", rows).
			Msgf("%d rows cannot be spread over %d pages of %s. Leaving its width alone", rows, pages, table.Name)
		return 0
	}

	// A page holds a whole number of rows, so not every page count can be
	// asked for. Saying which one this width actually reaches is the
	// difference between a target that was met and one that looks like it was.
	if reached != pages {
		log.Warn().Str("table", table.Name).Int64("askedPages", pages).Int64("reachedPages", reached).Int64("bytesPerRow", bytes).
			Msgf("%s cannot be spread over exactly %d pages: a page holds a whole number of rows, and the nearest reachable count for %d rows is %d, at %d bytes per row", table.Name, pages, rows, reached, bytes)
		return bytes
	}
	log.Info().Str("table", table.Name).Int64("pages", pages).Int64("rows", rows).Int64("bytesPerRow", bytes).
		Msgf("aiming %s at %d pages: %d rows over %d pages is %d bytes per row", table.Name, pages, rows, pages, bytes)
	return bytes
}

func tableNames(tables []*db.Table) []string {
	names := make([]string, 0, len(tables))
	for _, table := range tables {
		names = append(names, table.FullName())
	}
	return names
}

// resolveForeignKeyParents makes sure every table this run points a foreign
// key at holds something to point at.
//
// A --query names the tables it reads, and those tables have NOT NULL foreign
// keys to tables it does not name. The run used to discover that at the moment
// it tried to sample one -- "table public.categories is empty, so there is
// nothing to point a foreign key at" -- by which point the tables ahead of it
// in the insert order were already loaded, and repeating the run meant
// emptying them again first.
//
// The full closure is known here: loading a table loads the tables its keys
// point at, and theirs in turn. So either those tables are pulled into the run
// or the run is refused naming all of them at once, before a row is written.
//
// A parent that already holds rows is left alone, which is the other half of
// it: filling a child against a dimension table loaded by an earlier run is an
// ordinary thing to do and has never needed the parent to be reloaded.
func (cmd *RunCmd) resolveForeignKeyParents(tables []*db.Table) ([]*db.Table, error) {
	inRun := func(name string) bool {
		return slices.ContainsFunc(tables, func(t *db.Table) bool { return strings.EqualFold(t.Name, name) })
	}

	type need struct{ parent, child string }
	missing := []need{}
	seen := map[string]bool{}

	// An empty parent has to be walked into whether or not it joins the run:
	// filling it needs its own parents filled, and a refusal that names one
	// level at a time is the diagnose-and-retry cycle this exists to remove.
	// So the walk has its own queue, and only the run's table list is guarded
	// by --fill-fk-parents.
	queue := append([]*db.Table{}, tables...)
	for i := 0; i < len(queue); i++ {
		for _, constraint := range queue[i].Constraints {
			// a table pointing at itself is in the run by definition, and the
			// run already knows how to break that loop
			if constraint.IsSelfReferencing() || constraint.ReferencedTable == nil {
				continue
			}
			if inRun(constraint.ReferencedTableName) {
				continue
			}

			parent := constraint.ReferencedTable
			if seen[parent.FullName()] {
				continue
			}
			seen[parent.FullName()] = true

			filled, err := db.HasAnyRow(parent.Schema, parent.Name)
			if err != nil {
				return nil, err
			}
			if filled {
				log.Debug().Str("table", queue[i].Name).Str("parent", parent.FullName()).
					Msg("a foreign key points outside this run, at a table that already holds rows")
				continue
			}
			queue = append(queue, parent)

			if !cmd.FillFKParents {
				missing = append(missing, need{parent: parent.FullName(), child: queue[i].FullName()})
				continue
			}

			rows := cmd.Rows.For(parent.Name)
			log.Info().Str("table", parent.FullName()).Int64("rows", rows).Str("neededBy", queue[i].FullName()).
				Msgf("adding %s to this run, as --fill-fk-parents asks: %s points at it and it holds no row. It will be filled with %d rows",
					parent.FullName(), queue[i].FullName(), rows)
			tables = append(tables, parent)
		}
	}

	if len(missing) == 0 {
		return tables, nil
	}

	named := make([]string, 0, len(missing))
	for _, m := range missing {
		named = append(named, fmt.Sprintf("%s (pointed at by %s)", m.parent, m.child))
	}
	return nil, errors.Errorf("this run points foreign keys at tables it does not fill, and they hold no row: %s. A foreign key has nothing to point at, so the run would fail part way through with the tables ahead of them already loaded. Fill them first, or add them to this run with --fill-fk-parents", strings.Join(named, ", "))
}

// reportPredicatesNothingWillMatch names the literals the query filters on that
// no row is going to hold.
//
// --query-param-freq is off by default, so a predicate on a column nothing else
// speaks for matches nothing and the query comes back empty. That is the honest
// outcome -- inserting a value on 10% of the rows because a query mentions it
// is a selectivity nobody measured, and a plan built on one is wrong in a way
// no row count shows -- but it is only useful if the run says which predicate
// it was, rather than leaving an empty result to be worked back from.
func reportPredicatesNothingWillMatch(tables []*db.Table, queryParams map[string][]string) {
	for tableColumn, values := range queryParams {
		table, column, ok := resolveQueryParameter(tables, tableColumn)
		if !ok {
			continue
		}

		// A key holds whatever its parent holds. No option puts a chosen value
		// in it, so pointing at --query-param-freq would be wrong advice.
		if table.IsFieldInAnyConstraints(*column) {
			log.Warn().Str("table", table.Name).Str("column", column.ColumnName).Strs("values", values).
				Msgf("%s.%s is a foreign key, so it holds what its parent holds and the query's %v cannot be put in it. The query only matches if the parent was filled with those values",
					table.Name, column.ColumnName, values)
			continue
		}

		unmatched := []string{}
		for _, value := range values {
			if !frequency.WillInsert(table.Name, column.ColumnName, value) {
				unmatched = append(unmatched, value)
			}
		}
		if len(unmatched) == 0 {
			continue
		}
		log.Warn().Str("table", table.Name).Str("column", column.ColumnName).Strs("values", unmatched).
			Msgf("nothing will put %v in %s.%s, so the query filtering on it returns no row. --query-param-freq=0.1 inserts each of them on 10%% of the rows, --values-freq-map sets a rate per value, and --stat-file uses the rates an export measured",
				unmatched, table.Name, column.ColumnName)
	}
}

// resolveQueryParameter finds the column a "table.column" key of the parsed
// query stands for, among the tables this run fills.
func resolveQueryParameter(tables []*db.Table, tableColumn string) (*db.Table, *db.Field, bool) {
	name, columnName, found := strings.Cut(tableColumn, ".")
	if !found {
		return nil, nil, false
	}
	for _, table := range tables {
		if !strings.EqualFold(table.Name, name) {
			continue
		}
		field := table.FieldByName(columnName)
		if field == nil {
			return nil, nil, false
		}
		return table, field, true
	}
	return nil, nil, false
}

// reportUnsupportedFields says out loud which columns this run cannot fill,
// and refuses the run when leaving one out cannot work.
//
// A column of a type no generator knows is left out of the INSERT. Nothing
// used to be said about it beyond one line at Error level, and --dry-run
// printed an INSERT without the column, which looks correct:
//
//   - nullable, or holding a default: the run succeeds and the column is
//     entirely NULL or entirely its default. The rows are narrower than the
//     ones being reproduced, which is what a plan-fidelity reproduction
//     measures, so it is worth a warning.
//   - NOT NULL with no default: the engine rejects every insert, naming a
//     column the user never mentioned. It is refused here instead, before a
//     single row is written and in --dry-run too.
func reportUnsupportedFields(tables []*db.Table) error {
	reported := map[string]struct{}{}
	refused := []string{}

	for _, table := range tables {
		for _, field := range table.FieldsUnsupported() {
			name := table.Schema + "." + table.Name + "." + field.ColumnName
			if _, alreadyReported := reported[name]; alreadyReported {
				continue
			}
			reported[name] = struct{}{}

			if !field.IsNullable && !field.HasDefaultValue {
				refused = append(refused, fmt.Sprintf("%s (%s)", name, field.DataType))
				continue
			}
			log.Warn().Str("table", table.Name).Str("column", field.ColumnName).Str("type", field.DataType).
				Msgf("no value can be generated for %s of type %s, it is left out of the INSERT and every row will get its default (NULL unless it has one). The rows will be narrower than the ones being reproduced", name, field.DataType)
		}
	}

	if len(refused) > 0 {
		return errors.Errorf("no value can be generated for %s, and the column is NOT NULL with no default, so every insert would be rejected. Give the column a default, drop it, or leave it out of this run by naming the columns to fill in a --query", strings.Join(refused, ", "))
	}
	return nil
}

// mergeStats reuses the statistics the database already collected on a
// populated table to set the null and value frequencies for this run.
func (cmd *RunCmd) mergeStats(tables []*db.Table) error {
	stats, err := frequency.LoadStats(cmd.StatFile)
	if err != nil {
		return err
	}

	frequency.MergeStats(stats, func(cs frequency.ColumnStats) (frequency.Target, bool) {
		for _, table := range tables {
			if !strings.EqualFold(table.Name, cs.Tablename) {
				continue
			}
			if cs.Schemaname != "" && table.Schema != "" && !strings.EqualFold(table.Schema, cs.Schemaname) {
				continue
			}
			field := table.FieldByName(cs.Attname)
			if field == nil {
				return frequency.Target{}, false
			}
			// Whether the column is a key decides what of the dump can be
			// reused for it, which is why this is settled here: the foreign
			// keys are only all known once the guessed ones have been added.
			return frequency.Target{
				Table:      table.Name,
				Column:     field.ColumnName,
				ForeignKey: table.IsFieldInAnyConstraints(*field),
			}, true
		}
		return frequency.Target{}, false
	})
	return nil
}

func (cmd *RunCmd) run(table *db.Table) error {
	rows := cmd.Rows.For(table.Name)
	colNullFreqs := frequency.SharedTableFrequency[table.Name]
	ins := generate.New(table, cmd.ForeignKeyLinks, cmd.WorkersCount, cmd.MaxTextSize, cmd.UUIDVersion, colNullFreqs, &cmd.MinGeneratedTime, &cmd.MaxGeneratedTime)
	ins.SetTargetBytesPerRow(cmd.rowWidthTarget(table, rows))

	if !cmd.Quiet && !cmd.DryRun {
		go startProgressBar(table.Name, rows, ins.NotifyChan)
	}

	if cmd.DryRun {
		return ins.DryRun(rows, cmd.BulkSize)
	}

	err := ins.Run(rows, cmd.BulkSize)
	if err != nil {
		// A worker may still be finishing the bulk it was given, and reporting
		// its progress on that channel: closing it under a worker still
		// writing to it is a panic. The run is over, so the progress bar is
		// left where it stands.
		return err
	}
	close(ins.NotifyChan)
	return nil
}

func startProgressBar(tablename string, total int64, c chan int64) {
	writer := goterminal.New(os.Stdout)
	var count int64
	for n := range c {
		count += n
		writer.Clear()
		fmt.Fprintf(writer, "Writing %s (%d/%d) rows...\n", tablename, count, total)
		writer.Print() //nolint
	}
	writer.Reset()
}

// applyNullFreq spreads --null-freq out: the bare number becomes the fraction
// every nullable column falls back to, and each "table.column=" entry is
// recorded against that column, marked as coming from the command line so a
// --stat-file read afterwards does not overwrite what was asked for.
//
// A null fraction belongs to a column rather than to a table -- a table is not
// the thing that can be NULL -- so an entry naming only a table is refused
// here rather than silently matching nothing.
func (cmd *RunCmd) applyNullFreq() error {
	frequency.DefaultNullFrequency = cmd.NullFreq.For("")

	for _, key := range cmd.NullFreq.Columns() {
		table, column, ok := strings.Cut(key, ".")
		if !ok || table == "" || column == "" {
			return errors.Errorf("--null-freq=%q names no column: a null fraction is set per column, written \"table.column=0.63\", or as a bare number for every column", key)
		}
		freq := cmd.NullFreq[key]
		if freq < 0 || freq > 1 {
			return errors.Errorf("--null-freq for %s.%s is %g, which is not a fraction between 0 and 1", table, column, freq)
		}
		frequency.SetNullFromFlag(table, column, freq)
	}
	if freq := frequency.DefaultNullFrequency; freq < 0 || freq > 1 {
		return errors.Errorf("--null-freq is %g, which is not a fraction between 0 and 1", freq)
	}
	return nil
}

func helperForMySQLFKChecks(tablesSorted []*db.Table, err error) {

	// getting the table provoking the issue from the deepest error
	tableRegex := regexp.MustCompile("REFERENCES `(\\w+)`")
	submatches := tableRegex.FindStringSubmatch(errors.Cause(err).Error())

	// checking if this table is supposed to be in our list
	if len(submatches) == 2 && !slices.ContainsFunc(tablesSorted, func(t *db.Table) bool {
		return strings.ToLower(t.Name) == submatches[1]
	}) {
		log.Warn().Msg("A foreign key pointing to a missing tables forced an error. Hint: SET GLOBAL foreign_key_checks=0;")
	}
}
