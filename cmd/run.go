package cmd

import (
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

	Table    string
	Rows     int64 `name:"rows" required:"true" help:"Number of rows to insert"`
	BulkSize int64 `name:"bulk-size" help:"Number of rows per insert statement" default:"1000"`
	DryRun   bool  `name:"dry-run" help:"Print queries to the standard output instead of inserting them into the db"`
	Quiet    bool  `name:"quiet" help:"Do not print progress bar"`

	Query     string
	QueryFile string

	generate.ForeignKeyLinks
}

// Run starts inserting data.
func (cmd *RunCmd) Run() error {
	_, err := db.Connect(cmd.DB)
	if err != nil {
		return err
	}

	table, err := db.LoadTable(cmd.DB.Database, cmd.Table)
	if err != nil {
		return err
	}

	var (
		tables, identifiers map[string]struct{}
		joins               map[string]string
	)
	if cmd.Query != "" || cmd.QueryFile != "" {
		tables, identifiers, joins, err = data.ParseQuery(cmd.Query, cmd.QueryFile, cmd.DB.Engine)
		if err != nil {
			return err
		}
		log.Debug().Interface("identifiers", identifiers).Interface("joins", joins).Msg("query parsed")
		table.SkipBasedOnIdentifiers(identifiers)
		table.AddVirtualFKs(joins)
	}
	_ = tables
	_, err = cmd.run(table)
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
