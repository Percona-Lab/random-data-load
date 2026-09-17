package main

import (
	"fmt"
	"os"
	"runtime/pprof"

	"net/http"
	_ "net/http/pprof"

	"github.com/Percona-Lab/random-data-load/cmd"
	"github.com/Percona-Lab/random-data-load/frequency"
	"github.com/Percona-Lab/random-data-load/generate"
	"github.com/Percona-Lab/random-data-load/query"
	"github.com/alecthomas/kong"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	toolname = "random-data-load"
)

var (
	Build     string //nolint
	GoVersion string //nolint
	Version   string //nolint
	Commit    string //nolint
)

var buildInfo = fmt.Sprintf("%s\nVersion %s\nBuild: %s using %s\nCommit: %s", toolname, Version, Build, GoVersion, Commit)

var cli struct {
	Run   cmd.RunCmd   `cmd:"run" help:"Starts the insert process. --help-tuning explains what each tuning flag moves"`
	Query cmd.QueryCmd `cmd:"query" help:"Providing a query will analyze its schema usage, insert recursively into tables, and identify implicit joins"`

	ExportStat cmd.ExportStatCmd `cmd:"export-stat" help:"Print the command exporting the column statistics a --query needs, to be replayed with 'run --stat-file'"`

	ExplainStat cmd.ExplainStatCmd `cmd:"explain-stat" help:"Read a reported EXPLAIN and print the row counts, page counts, selectivities and distinct counts it implies, as flags for 'run'"`

	Verify cmd.VerifyCmd `cmd:"verify" help:"Read a filled database back and print what it holds next to what was asked for: row counts, page counts, selectivities, distinct counts and column statistics, side by side"`

	Version     kong.VersionFlag
	Profile     bool   `name:"pprof" help:"generate pprof trace at --cpu-prof-path. Also opens port 6060 for pprof go tool"`
	CPUProfPath string `name:"cpu-prof-path" default:"cpu.prof"`
	Debug       bool   `name:"debug"`
}

func main() {
	kongcli := kong.Parse(&cli,
		kong.Name(toolname),
		kong.Description("Load random data into a table"),
		kong.UsageOnError(),
		kong.ValueMapper(&cli.Run.AddForeignKeys, query.VirtualJoins{}),
		kong.ValueMapper(&cli.Run.ValuesFreqMap, &frequency.FrequencyIndexValuesParameter{}),
		// row counts are given once for the run, per table, or both
		kong.ValueMapper(&cli.Run.Rows, &generate.PerTableInt{}),
		kong.ValueMapper(&cli.Verify.Rows, &generate.PerTableInt{}),
		kong.ValueMapper(&cli.ExplainStat.Rows, &generate.PerTableInt{}),
		// a null fraction the same way, but its subject is a column
		kong.ValueMapper(&cli.Run.NullFreq, &generate.PerTableFloat{}),
		// every sampler's tuning takes a value per parent table as well as one
		// for the whole run, so each needs its own reader
		kong.ValueMapper(&cli.Run.CoinFlipPercent, &generate.PerTableFloat{}),
		kong.ValueMapper(&cli.Run.NormalStddev, &generate.PerTableFloat{}),
		kong.ValueMapper(&cli.Run.NormalMean, &generate.PerTableFloat{}),
		kong.ValueMapper(&cli.Run.ParetoS, &generate.PerTableFloat{}),
		kong.ValueMapper(&cli.Run.ParetoV, &generate.PerTableFloat{}),
		// and so does a row width target, which describes the table being
		// filled rather than a parent being sampled
		kong.ValueMapper(&cli.Run.TargetBytesPerRow, &generate.PerTableFloat{}),
		kong.ValueMapper(&cli.Run.TargetRelpages, &generate.PerTableFloat{}),
		kong.Vars{
			"version":        buildInfo,
			"SequentialFlag": generate.SequentialFlag,
			"BinomialFlag":   generate.BinomialFlag,
			"NormalFlag":     generate.NormalFlag,
			"ParetoFlag":     generate.ParetoFlag,
		},
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: false,
			Summary: true,
			Tree:    true,
		}),
	)
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if cli.Debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	if cli.Profile {
		f, err := os.Create(cli.CPUProfPath)
		if err != nil {
			panic(err)
		}
		defer f.Close()

		// for other types of profiles like mutexes, mem
		go func() {
			http.ListenAndServe("localhost:6060", nil)
		}()

		// Start CPU profiling
		if err := pprof.StartCPUProfile(f); err != nil {
			panic(err)
		}
		defer pprof.StopCPUProfile()
	}

	err := kongcli.Run()
	kongcli.FatalIfErrorf(err)
}
