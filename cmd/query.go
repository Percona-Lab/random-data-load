package cmd

import (
	"fmt"

	"github.com/ylacancellera/random-data-load/query"
)

type QueryCmd struct {
	Query  string `required:""`
	Engine string `enum:"mysql,pg" required:""`
}

func (cmd *QueryCmd) Run() error {
	var (
		tables, identifiers map[string]struct{}
		//joins               map[string]string
		joins       []query.VirtualJoin
		queryParams map[string][]string
		err         error
	)
	tables, identifiers, joins, queryParams, err = query.ParseQuery(cmd.Query, cmd.Engine, false)
	if err != nil {
		return err
	}
	fmt.Println("tables", tables)
	fmt.Println("joins", joins)
	fmt.Println("identifiers", identifiers)
	fmt.Println("queryParams", queryParams)
	return nil
}
