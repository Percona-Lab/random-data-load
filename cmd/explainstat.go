package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/Percona-Lab/random-data-load/explain"
	"github.com/Percona-Lab/random-data-load/generate"
	"github.com/pkg/errors"
)

// ExplainStatCmd reads a plan and prints the statistics behind it.
//
// Reproducing a plan means matching the numbers the planner read, and the plan
// carries most of them: a sequential scan's cost gives the page count, a
// filter's removed rows give a selectivity, an aggregate's estimate gives a
// distinct count. Working those out by hand is the same arithmetic every time,
// and getting it wrong is not obvious afterwards.
type ExplainStatCmd struct {
	Engine string `enum:"mysql,pg" required:"" help:"mysql,pg"`

	File string `arg:"" optional:"" type:"path" help:"File holding the EXPLAIN output. Reads standard input when omitted."`

	Rows generate.PerTableInt `name:"rows" help:"Row counts the reported side gave, in the same format --rows takes on \"run\": --rows=\"orders=500000;order_items=1500000\". A table this plan only reaches through an index never reveals its size, and a predicate on it has no selectivity without one." default:""`

	FlagsOnly bool `name:"flags-only" help:"Print just the flags for the run, ready to paste."`
}

func (cmd *ExplainStatCmd) Run() error {
	if err := cmd.supported(); err != nil {
		return err
	}

	text, err := cmd.read()
	if err != nil {
		return err
	}

	plan := explain.Parse(text)
	if len(plan.Nodes) == 0 {
		return errors.New("no plan node found. This reads the text EXPLAIN prints, the default format, with or without ANALYZE. Pass the plan as postgres printed it, without the psql table borders")
	}

	stats := plan.Derive(cmd.Rows.Named())

	if cmd.FlagsOnly {
		if flag := stats.RowsFlag(); flag != "" {
			fmt.Printf("--rows=%q\n", flag)
		}
		if flag := stats.ValuesFreqMapFlag(); flag != "" {
			fmt.Printf("--values-freq-map=%q\n", flag)
		}
		return nil
	}

	fmt.Print(stats.Report())
	return nil
}

func (cmd *ExplainStatCmd) read() (string, error) {
	if cmd.File == "" {
		text, err := io.ReadAll(os.Stdin)
		return string(text), errors.Wrap(err, "reading the plan from standard input")
	}
	text, err := os.ReadFile(cmd.File)
	return string(text), errors.Wrapf(err, "reading the plan from %s", cmd.File)
}

// supported reports what this command can read.
//
// Only postgres prints the costs and row counts this works from. mysql's
// EXPLAIN ANALYZE is a different format with no page counts in it, and its
// plain EXPLAIN carries estimates without the actuals that make a selectivity.
func (cmd *ExplainStatCmd) supported() error {
	switch cmd.Engine {
	case "pg":
		return nil
	case "mysql":
		return errors.New("--engine=mysql cannot be read yet: this works from the costs and actual row counts in a postgres text plan, and mysql's EXPLAIN ANALYZE gives neither page counts nor rows-removed counts. Set the frequencies by hand with --null-freq and --values-freq-map")
	}
	return errors.Errorf("unimplemented engine %q", cmd.Engine)
}
