package cmd

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"slices"

	"github.com/apoorvam/goterminal"
	"github.com/rs/zerolog/log"
	"github.com/ylacancellera/random-data-load/data"
	"github.com/ylacancellera/random-data-load/db"
	"github.com/ylacancellera/random-data-load/generate"
)

type RunCmd struct {
	DB db.Config `embed:""`

	Table    string `help:"which table to insert to. It will be ignored when a query is included with either --query or --query-file"`
	Rows     int64  `name:"rows" required:"true" help:"Number of rows to insert"`
	BulkSize int64  `name:"bulk-size" help:"Number of rows per insert statement" default:"1000"`
	DryRun   bool   `name:"dry-run" help:"Print queries to the standard output instead of inserting them into the db"`
	Quiet    bool   `name:"quiet" help:"Do not print progress bar"`

	Query     string `help:"providing a query will enable to automatically discover the schema, insert recursively into tables, anticipate joins"`
	QueryFile string `help:"see --query. Accepts a path instead of a direct query"`

	generate.ForeignKeyLinks
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

	tablesSorted := []*db.Table{}
	for tableKey := range tablesNames {
		table, err := db.LoadTable(cmd.DB.Database, tableKey)
		if err != nil {
			return err
		}

		if cmd.hasQuery() {
			table.SkipBasedOnIdentifiers(identifiers)
		}
		tablesSorted = append(tablesSorted, table)
	}
	db.FilterVirtualFKs(tablesSorted, joins)
	db.AddVirtualFKs(tablesSorted, joins)

	// sort from the tables with least constraints to table with most constraint
	// TODO: could disable the FK for which we don't have the table anyway
	slices.SortFunc(tablesSorted, func(a, b *db.Table) int {
		return len(a.Constraints) - len(b.Constraints)
	})

	for _, table := range tablesSorted {
		log.Debug().Str("table", table.Name).Int("number of constraint", len(table.Constraints)).Msg("tables sorted")
	}

	for _, table := range tablesSorted {
		_, err = cmd.run(table)
	}

	return err
}

func (cmd *RunCmd) run(table *db.Table) (int64, error) {
	ins := generate.New(table, cmd.ForeignKeyLinks)
	wg := &sync.WaitGroup{}

	if !cmd.Quiet && !cmd.DryRun {
		wg.Add(1)
		startProgressBar(cmd.Rows, ins.NotifyChan(), wg)
	}

	if cmd.DryRun {
		return ins.DryRun(cmd.Rows, cmd.BulkSize)
	}

	n, err := ins.Run(cmd.Rows, cmd.BulkSize)
	wg.Wait()
	return n, err
}

func (cmd *RunCmd) hasQuery() bool {
	return cmd.Query != "" || cmd.QueryFile != ""
}

func startProgressBar(total int64, c chan int64, wg *sync.WaitGroup) {
	go func() {
		writer := goterminal.New(os.Stdout)
		var count int64
		for n := range c {
			count += n
			writer.Clear()
			fmt.Fprintf(writer, "Writing (%d/%d) rows...\n", count, total)
			writer.Print() //nolint
		}
		writer.Reset()
		wg.Done()
	}()
}
