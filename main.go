package main

import (
	"fmt"
	"log"
	"net/http"

	_ "net/http/pprof"

	"github.com/alecthomas/kong"
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
	Run     cmd.RunCmd `cmd:"run" help:"Starts the insert process"`
	Version kong.VersionFlag
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
	// Server for pprof
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()

	err := kongcli.Run()
	kongcli.FatalIfErrorf(err)
}
