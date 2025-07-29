package cmd

import (
	"errors"
	"fmt"

	"github.com/ylacancellera/random-data-load/data"
)

type QueryCmd struct {
	Query     string
	QueryFile string
	Engine    string `enum:"mysql,pg" required:""`
}

func (cmd *QueryCmd) Run() error {
	var (
		tables, identifiers map[string]struct{}
		joins               map[string]string
		err                 error
	)
	if cmd.Query == "" && cmd.QueryFile == "" {
		return errors.New("Need --query or --query-file")
	}
	tables, identifiers, joins, err = data.ParseQuery(cmd.Query, cmd.QueryFile, cmd.Engine)
	if err != nil {
		return err
	}
	fmt.Println("tables", tables)
	fmt.Println("joins", joins)
	fmt.Println("identifiers", identifiers)
	return nil
}
