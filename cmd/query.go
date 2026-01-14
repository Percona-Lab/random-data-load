package cmd

import (
	"fmt"

	"github.com/ylacancellera/random-data-load/data"
)

type QueryCmd struct {
	Query  string `required:""`
	Engine string `enum:"mysql,pg" required:""`
}

func (cmd *QueryCmd) Run() error {
	var (
		tables, identifiers map[string]struct{}
		joins               map[string]string
		err                 error
	)
	tables, identifiers, joins, err = data.ParseQuery(cmd.Query, cmd.Engine, false)
	if err != nil {
		return err
	}
	fmt.Println("tables", tables)
	fmt.Println("joins", joins)
	fmt.Println("identifiers", identifiers)
	return nil
}
