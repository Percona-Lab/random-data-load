package cmd

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/apoorvam/goterminal"
	"github.com/rs/zerolog/log"
	"github.com/ylacancellera/random-data-load/data"
	"github.com/ylacancellera/random-data-load/db"
	"github.com/ylacancellera/random-data-load/generate"
)

type RunCmd struct {
	DB db.Config `embed:""`

	Table        string `help:"Table to insert to. When using --query, --table will be used to restrict the tables to insert to."`
	Rows         int64  `name:"rows" required:"true" help:"Number of rows to insert"`
	BulkSize     int64  `name:"bulk-size" help:"Number of rows per insert statement" default:"1000"`
	DryRun       bool   `name:"dry-run" help:"Print queries to the standard output instead of inserting them into the db"`
	Quiet        bool   `name:"quiet" help:"Do not print progress bar"`
	WorkersCount int    `name:"workers" help:"How many workers to spawn. Only the random generation and sampling are parallelized. Insert queries are executed one at a time" default:"3"`
	MaxTextSize  int64  `help:"Limit the maximum size of long text, varchar and blob fields." default:"65535"`

	Query string `help:"Providing a query will enable to automatically discover the schema, insert recursively into tables, enforce implicit joins."`

	generate.ForeignKeyLinks
	AddForeignKeys map[string]string `name:"add-foreign-keys" help:"Add foreign keys, if they are not explicitely created in the table schema. The format must be parent_table.col1=child_table.col2. It can complement the foreign keys guessed from the --query, or be used to manually define foreign keys when using --no-fk-guess too. Example --add-foreign-keys=\"customers.id=purchases.customer_id;purchases.id=items.purchase_id\"" `
	NoFKGuess      bool              `name:"no-fk-guess" help:"Do not try to guess foreign keys from the --query missing in the schema. When a query is provided, it will analyze the expected JOINs and try to respect dependencies even when foreign keys are not explicitely created in the database objects. This flag will make the tool stick to the constraints defined in the database only, unless you add foreign keys manually with --add-foreign-keys." `
	NoSkipFields   bool              `name:"no-skip-fields" help:"Disable field whitelist system. When using a --query, it will get the list of fields being used as a whitelist in order to generate the minimal sets of fields required, unless --no-skip-fields is being used or any * has been found."`
	NullFrequency  int64             `name:"null-frequency" help:"Define how frequent nullable fields should be NULL." default:"10"`
}

// Run starts inserting data.
func (cmd *RunCmd) Run() error {

	// Quick check to confirm database connection
	_, err := db.Connect(cmd.DB)
	if err != nil {
		return err
	}

	generate.NullFrequency = cmd.NullFrequency

	tablesNames := map[string]struct{}{}
	identifiers := map[string]struct{}{}
	joins := map[string]string{}

	if cmd.Query == "" && cmd.Table == "" {
		return errors.New("Need either a --query or a --table")
	}

	if cmd.Query != "" {
		tablesNames, identifiers, joins, err = data.ParseQuery(cmd.Query, cmd.DB.Engine, cmd.NoFKGuess)
		if err != nil {
			return err
		}
		log.Debug().Interface("identifiers", identifiers).Interface("joins", joins).Msg("query parsed")
	}
	// if --table is given, we will restrict inserts to this table only
	// we will still skip some columns and potentially have virtual FKs
	if cmd.Table != "" {
		tablesNames = map[string]struct{}{cmd.Table: struct{}{}}
	}

	for parent, child := range cmd.AddForeignKeys {
		joins[parent] = child
	}

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
	// now we have the full table list, we can autocomplete foreign keys
	if len(joins) > 0 {
		db.FilterVirtualFKs(tables, joins)
		db.AddVirtualFKs(tables, joins)
	}
	// and identify which constraints should be "garanteed" for this run
	for _, table := range tables {
		table.FlagConstraintThatArePartsOfThisRun(tables)
	}
	// so that we can sort based on the dependencies we need to satisfy
	tablesSorted := db.SortTables(tables)

	for _, table := range tablesSorted {
		log.Debug().Str("table", table.Name).Int("number of constraint", len(table.Constraints)).Msg("tables sorted")
	}

	// one at a time.
	// Parallelizing here will complexify the foreign links, for probably not so much gain
	for _, table := range tablesSorted {
		err = cmd.run(table)
		if err != nil {
			return err
		}
	}

	return err
}

func (cmd *RunCmd) run(table *db.Table) error {
	ins := generate.New(table, cmd.ForeignKeyLinks, cmd.WorkersCount, cmd.MaxTextSize)
	wg := &sync.WaitGroup{}

	if !cmd.Quiet && !cmd.DryRun {
		wg.Add(1)
		go startProgressBar(table.Name, cmd.Rows, ins.NotifyChan(), wg)
	}

	if cmd.DryRun {
		return ins.DryRun(cmd.Rows, cmd.BulkSize)
	}

	err := ins.Run(cmd.Rows, cmd.BulkSize)
	wg.Wait()
	return err
}

func startProgressBar(tablename string, total int64, c chan int64, wg *sync.WaitGroup) {
	writer := goterminal.New(os.Stdout)
	var count int64
	for n := range c {
		count += n
		writer.Clear()
		fmt.Fprintf(writer, "Writing %s (%d/%d) rows...\n", tablename, count, total)
		writer.Print() //nolint
	}
	writer.Reset()
	wg.Done()
}
