package main

import (
	"fmt"
	"os"
	"runtime/pprof"

	_ "net/http/pprof"

	"github.com/alecthomas/kong"
	"github.com/rs/zerolog"
	"github.com/ylacancellera/random-data-load/cmd"
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
	Run         cmd.RunCmd `cmd:"run" help:"Starts the insert process"`
	Version     kong.VersionFlag
	Profile     bool   `name:"pprof"`
	CPUProfPath string `name:"cpu-prof-path" default:"cpu.prof"`
	Debug       bool   `name:"debug"`
}

func main() {
	kongcli := kong.Parse(&cli,
		kong.Name(toolname),
		kong.Description("Load random data into a table"),
		kong.UsageOnError(),
		kong.Vars{
			"version": buildInfo,
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

	if cli.Profile {
		f, err := os.Create(cli.CPUProfPath)
		if err != nil {
			panic(err)
		}
		defer f.Close()

		// Start CPU profiling
		if err := pprof.StartCPUProfile(f); err != nil {
			panic(err)
		}
		defer pprof.StopCPUProfile()
	}

	err := kongcli.Run()
	kongcli.FatalIfErrorf(err)
}
