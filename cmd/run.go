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

	Table        string `help:"which table to insert to. It will be ignored when a query is included with either --query or --query-file"`
	Rows         int64  `name:"rows" required:"true" help:"Number of rows to insert"`
	BulkSize     int64  `name:"bulk-size" help:"Number of rows per insert statement" default:"1000"`
	DryRun       bool   `name:"dry-run" help:"Print queries to the standard output instead of inserting them into the db"`
	Quiet        bool   `name:"quiet" help:"Do not print progress bar"`
	WorkersCount int    `name:"workers" help:"how many workers to spawn. Only the random generation and sampling are parallelized. Insert queries are executed one at a time" default:"3"`

	Query     string `help:"providing a query will enable to automatically discover the schema, insert recursively into tables, anticipate joins"`
	QueryFile string `help:"see --query. Accepts a path instead of a direct query"`

	generate.ForeignKeyLinks
	VirtualForeignKeys         map[string]string `name:"virtual-foreign-keys" help:"add additional foreign keys, if they are not explicitely created in the table schema. The format must be parent_table.col1=child_table.col2. It will overwrite every JOINs guessed from queries. Example --virtual-foreign-keys=\"customers.id=purchases.customer_id;purchases.id=items.purchase_id\"" xor:"virtualfk"`
	SkipAutoVirtualForeignKeys bool              `name:"skip-auto-virtual-foreign-keys" help:"disable foreign key autocomplete. When a query is provided, it will analyze the expected JOINs and try to respect dependencies even when foreign keys are not explicitely created in the database objects. This flag will make the tool stick to the constraints defined in the database only." xor:"virtualfk"`
}

// Run starts inserting data.
func (cmd *RunCmd) Run() error {
	_, err := db.Connect(cmd.DB)
	if err != nil {
		return err
	}

	tablesNames := map[string]struct{}{}
	identifiers := map[string]struct{}{}
	joins := map[string]string{}

	if !cmd.hasQuery() && cmd.Table == "" {
		return errors.New("Need either a query (--query | --query-file) or a table (--table)")
	}

	if cmd.hasQuery() {
		tablesNames, identifiers, joins, err = data.ParseQuery(cmd.Query, cmd.QueryFile, cmd.DB.Engine)
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

	if len(cmd.VirtualForeignKeys) > 0 {
		joins = cmd.VirtualForeignKeys
	}

	// loading base tables
	tables := []*db.Table{}
	for tableKey := range tablesNames {
		table, err := db.LoadTable(cmd.DB.Database, tableKey)
		if err != nil {
			return err
		}

		if cmd.hasQuery() {
			table.SkipBasedOnIdentifiers(identifiers)
		}

		tables = append(tables, table)
	}
	// now we have the full table list, we can autocomplete foreign keys
	if !cmd.SkipAutoVirtualForeignKeys {
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
	ins := generate.New(table, cmd.ForeignKeyLinks, cmd.WorkersCount)
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

func (cmd *RunCmd) hasQuery() bool {
	return cmd.Query != "" || cmd.QueryFile != ""
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
