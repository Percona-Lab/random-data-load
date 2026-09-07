package cmd

import (
	"fmt"
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

	Table            string           `help:"Table to insert to. When using --query, --table will be used to restrict the tables to insert to."`
	Rows             int64            `name:"rows" required:"true" help:"Number of rows to insert"`
	RowsPerTable     map[string]int64 `name:"rows-per-table" help:"Number of rows to insert per-table. Will have priority over --rows. Format is \"{table}=X\"" default:""`
	BulkSize         int64            `name:"bulk-size" help:"Number of rows per insert statement" default:"1000"`
	DryRun           bool             `name:"dry-run" help:"Print queries to the standard output instead of inserting them into the db"`
	Quiet            bool             `name:"quiet" help:"Do not print progress bar"`
	WorkersCount     int              `name:"workers" help:"How many workers to spawn. Only the random generation and sampling are parallelized. Insert queries are executed one at a time" default:"3"`
	MaxTextSize      int64            `help:"Limit the maximum size of long text, varchar and blob fields." default:"65535"`
	UUIDVersion      int              `name:"uuid-version" help:"UUID v4 or v7 for uuid datatypes" default:"4" enum:"4,7"`
	MinGeneratedTime time.Time        `help:"Generated timestamps will be after this date. Format is RFC3339. Will default to --max-generated-time - 1 year"`
	MaxGeneratedTime time.Time        `help:"Generated timestamps will be before this date. Format is RFC3339. Will default to now()"`
	Query            string           `help:"Providing a query will enable to automatically discover the schema, insert recursively into tables, enforce implicit joins."`

	generate.ForeignKeyLinks
	AddForeignKeys  query.VirtualJoins                      `name:"add-fk" help:"Add foreign keys, if they are not explicitely created in the table schema. It can complement the foreign keys guessed from the --query, or be used to manually define foreign keys when using --no-fk-guess too. Format: --add-fk=\"parent_table.col1[,col2...]=child_table.colx[,coly...][; additional fk ]\". Example: --add-fk=\"customers.id,created_at=purchases.customer_id,created_at;purchases.id=items.purchase_id\""`
	NoFKGuess       bool                                    `name:"no-fk-guess" help:"Do not try to guess foreign keys from the --query missing in the schema. When a query is provided, it will analyze the expected JOINs and try to respect dependencies even when foreign keys are not explicitely created in the database objects. This flag will make the tool stick to the constraints defined in the database only, unless you add foreign keys manually with --add-fk." `
	NoSkipFields    bool                                    `name:"no-skip-fields" help:"Disable field whitelist system. When using a --query, it will get the list of fields being used as a whitelist in order to generate the minimal sets of fields required, unless --no-skip-fields is being used or any * has been found."`
	NullFreq        float64                                 `name:"null-freq" help:"Define how frequent nullable fields should be NULL by default." default:"0.1"`
	NullFreqMap     frequency.FrequencyNullParameter        `name:"null-freq-map" help:"Define how frequent nullable fields should be NULL for a given column, as a fraction between 0 and 1 like --null-freq. Will have priority over --null-freq. The format is \"--null-freq-map=t1.c1=0.73;t1.c2=0.04\" to set 73% or 4% of NULL for respective columns" default:""`
	ValuesFreqMap   frequency.FrequencyIndexValuesParameter `name:"values-freq-map" help:"Inject arbitrary values at fixed frequencies. The format is \"--values-freq-map=t1.c1=val1:0.75,val2:0.23;t1.c2=10:0.99\" so that val1 will be on 75% of rows and val2 on 23% for column c1" default:""` // TODO we're not checking if the total freq is above 1
	QueryParamsFreq float64                                 `name:"query-param-freq" help:"Frequency at which to insert arbitrary values guessed from the query parameters. = and IN operators are handled. Can be disabled when set to 0.0." default:"0.1"`
	StatFile        string                                  `name:"stat-file" help:"Scan a column statistics export and reuse its null_frac, most_common_vals and most_common_freqs as --null-freq-map and --values-freq-map. Use the \"export-stat\" subcommand to get the command producing that file." type:"path"`
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
	if (cmd.DefaultRelationship == generate.ParetoFlag || len(cmd.Pareto) > 0) && (cmd.ParetoS <= 1.0 || cmd.ParetoV < 1) {
		return errors.New("--pareto-s needs to be >1, --pareto-v needs to be >=1")
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

	frequency.DefaultNullFrequency = cmd.NullFreq
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
	if cmd.StatFile != "" {
		// After the tables are loaded, so that an exported column can be
		// matched against the real one: the catalog reports folded names, and
		// both the frequency map and the generator are keyed on the names it
		// gave back.
		if err := cmd.mergeStats(tables); err != nil {
			return err
		}
		log.Debug().Interface("freq-map", frequency.SharedTableFrequency).Msg("merged exported statistics into frequency map")
	}

	// we can autocomplete foreign keys
	joins = append(joins, cmd.AddForeignKeys...)
	if len(joins) > 0 {
		db.AddVirtualFKs(tables, joins)
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
			rows, ok := cmd.RowsPerTable[table.Name]
			if !ok {
				rows = cmd.Rows
			}
			log.Info().Str("table", table.Name).Int64("rows", rows/2).Msg("table has a self-referencing foreign key. Setting --rows to half for this table since we will insert twice to it to resolve the dependency.")
			cmd.RowsPerTable[table.Name] = rows / 2
			tables = append([]*db.Table{copiedTable}, tables...)

		} else if table.HasAnyConstraintLoop() {
			return errors.Errorf("table %s has a foreign key loop", table.Name)
		}
	}

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

	frequency.MergeStats(stats, func(cs frequency.ColumnStats) (string, string, bool) {
		for _, table := range tables {
			if !strings.EqualFold(table.Name, cs.Tablename) {
				continue
			}
			if cs.Schemaname != "" && table.Schema != "" && !strings.EqualFold(table.Schema, cs.Schemaname) {
				continue
			}
			field := table.FieldByName(cs.Attname)
			if field == nil {
				return "", "", false
			}
			return table.Name, field.ColumnName, true
		}
		return "", "", false
	})
	return nil
}

func (cmd *RunCmd) run(table *db.Table) error {
	rows := valueForTable(cmd.Rows, cmd.RowsPerTable, table.Name)
	colNullFreqs := frequency.SharedTableFrequency[table.Name]
	ins := generate.New(table, cmd.ForeignKeyLinks, cmd.WorkersCount, cmd.MaxTextSize, cmd.UUIDVersion, colNullFreqs, &cmd.MinGeneratedTime, &cmd.MaxGeneratedTime)

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

func valueForTable[E any](val E, valPerTable map[string]E, table string) E {
	if v, ok := valPerTable[table]; ok {
		return v
	}
	return val
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
