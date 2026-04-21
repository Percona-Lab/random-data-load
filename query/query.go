package query

import (
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"gitlab.com/dalibo/transqlate/ast"
	"gitlab.com/dalibo/transqlate/lexer"
	"gitlab.com/dalibo/transqlate/mysql"
	"gitlab.com/dalibo/transqlate/parser"
	"gitlab.com/dalibo/transqlate/rewrite"
)

var aliases = map[string]string{} // only help to identify implicit joins
var tables map[string]struct{}

// ParseQuery will return the list of tables, every raw identifiers used (including tables again), every joins it could detect, and a mapping of query parameters
func ParseQuery(query, engine string, skipJoins bool) (map[string]struct{}, map[string]struct{}, []VirtualJoin, map[string][]string, error) {

	var parsed ast.Node
	var err error

	switch engine {
	case "mysql":
		parsed, err = mysql.Engine().Parse("", query)
		if err != nil {
			return nil, nil, nil, nil, err
		}
	case "pg":
		parse := func(source, input string) (ast.Node, error) {
			return parser.Parse(lexer.New(source, input))
		}
		engine := rewrite.New("pg", rewrite.Parser(parse))
		parsed, err = engine.Parse("", query)
		if err != nil {
			return nil, nil, nil, nil, err
		}
	default:
		return nil, nil, nil, nil, errors.New("unimplemented engine")
	}

	tables = traverseTables(parsed)
	identifiers := traverseIdentifiers(parsed)
	joins := []VirtualJoin{}
	if !skipJoins {
		joins = traverseJoins(parsed)
	}
	queryParams := traverseQueryParameters(parsed)
	return tables, identifiers, joins, queryParams, nil
}

func traverseIdentifiers(n ast.Node) map[string]struct{} {
	identifiers := map[string]struct{}{}

	emptyMap := false

	// don't need to iterate over selected columns, joins, where, group bys
	// having every raw identifiers will be good enough since it's used as a whitelist
	// it might have collisions down the line, but at worst it would only generate data on some extra column
	traverser := func(n ast.Node) bool {
		switch n := n.(type) {
		case ast.Leaf:
			switch {
			case n.IsIdentifier():
				identifiers[n.Token.Str] = struct{}{}
			case n.Token.Type == lexer.Punctuation && n.Token.Raw == "*":
				log.Debug().Type("node", n).Str("function", "traverseIdentifiers").Msg("cancelling identifiers, found '*'")
				emptyMap = true
				return false
			}

		}
		return true
	}
	n.Traverse(traverser)
	if emptyMap {
		return map[string]struct{}{}
	}
	return identifiers
}

func traverseTables(n ast.Node) map[string]struct{} {
	tables := map[string]struct{}{}

	// we want every mentioned table names
	// aliases are not wanted
	traverser := func(n ast.Node) bool {
		switch n := n.(type) {
		case ast.Join:
			leftname := tableName(n.Left)
			rightname := tableName(n.Right)
			log.Debug().Str("leftname", leftname).Str("rightname", rightname).Type("node", n).Type("leftnode", n.Left).Type("rightnode", n.Right).Msg("tableTraverser")
			if leftname != "" {
				tables[leftname] = struct{}{}
			}
			if rightname != "" {
				tables[rightname] = struct{}{}
			}
		case ast.From:
			for _, item := range n.Tables {
				tablename := tableName(item.Expression)
				log.Debug().Str("tablename", tablename).Type("node", n).Type("item", item.Expression).Msg("tableTraverser")
				if tablename != "" {
					tables[tablename] = struct{}{}
				}
			}
		}
		return true
	}

	n.Traverse(traverser)
	return tables
}

func traverseJoins(n ast.Node) []VirtualJoin {

	joins := []VirtualJoin{}

	// joinsTraverser will guess joins
	// the goal is to find joins that are not defined in schemas with foreign keys
	// that way we will be able to create a fake FK in the tool and have matching data
	// Joins that are indirect (subqueries, CTE) are currently missed
	traverser := func(n ast.Node) bool {
		switch n := n.(type) {
		case ast.Join:
			if n.Condition == nil {
				return true
			}
			tmp := n.Condition.(ast.Where)
			for _, clause := range tmp.Conditions {

				switch clause := clause.Expression.(type) {
				//case ast.List
				case ast.Infix:
					//tmp := clause.Expression.(ast.Infix)
					leftTable, leftCol := getTableColFromInfix(clause.Left)
					rightTable, rightCol := getTableColFromInfix(clause.Right)
					log.Debug().Str("left", leftTable).Str("right", rightTable).Type("clause", clause).Msg("JoinTraverser")
					if leftTable == "" || rightTable == "" {
						log.Debug().Type("left type", clause.Left).Type("right type", clause.Right).Str("left table", leftTable).Str("right table", rightTable).Str("left col", leftCol).Str("right col", rightCol).Msg("left or right side is empty in JoinTraverser, skipping")
						continue
					}

					joins = append(joins, VirtualJoin{
						Left: VirtualJoinPart{
							Table:   leftTable,
							Columns: []string{leftCol},
						},
						Right: VirtualJoinPart{
							Table:   rightTable,
							Columns: []string{rightCol},
						},
					})
				default:
					log.Debug().Type("clause", clause).Msg("non-handled JoinTraverser")
				}
			}
		}
		return true
	}

	n.Traverse(traverser)
	return joins
}

func traverseQueryParameters(n ast.Node) map[string][]string {

	queryParams := map[string][]string{}

	traverser := func(n ast.Node) bool {
		switch n := n.(type) {
		case ast.Infix:
			switch {
			case n.Is("="):
				leftTable, leftCol := getTableColumnFromInfixOrLeaf(n.Left)
				id := leftTable + "." + leftCol
				switch right := n.Right.(type) {
				case ast.Leaf:
					queryParams[id] = append(queryParams[id], []string{right.String()}...)
				}
			case n.Is("IN"):
				leftTable, leftCol := getTableColumnFromInfixOrLeaf(n.Left)
				id := leftTable + "." + leftCol
				switch right := n.Right.(type) {
				case ast.List:
					values := []string{}
					for _, item := range right.Items {
						if val := getItemValue(item.Expression); val != "" {
							values = append(values, val)
						}
					}
					queryParams[id] = append(queryParams[id], values...)
				}
			}
		}
		return true
	}

	n.Traverse(traverser)
	return queryParams
}

// Differs from ast.Tablename for how alias are handled,
// JOINs are removed because they handled a layer above not to miss the left nodes
func tableName(expr ast.Node) string {
	switch expr := expr.(type) {
	case ast.Alias: // X.Y AS mytable, (SELECT ...) mytable, ...
		aliases[expr.Name.Str] = tableName(expr.Expression)
		return aliases[expr.Name.Str] // Return mytable.
	case ast.Leaf: // plain SELECT FROM mytable
		return expr.Token.Str // return mytable
	case ast.Infix: // SELECT FROM namespace.mytable.
		if expr.Is(".") {
			return tableName(expr.Right) // Return mytable
		}
	default:
		log.Debug().Type("node", expr).Msg("tableName unhandled type")
	}
	return "" // Anonymous table
}

func getTableColFromInfix(expr ast.Node) (string, string) {
	switch expr := expr.(type) {
	case ast.Infix:
		left := expr.Left.(ast.Leaf)
		right := expr.Right.(ast.Leaf)
		tablename := left.Token.Str
		if realTableName, ok := aliases[tablename]; ok {
			tablename = realTableName
		}
		if tablename == "" {
			return "", ""
		}
		return tablename, right.Token.Str
	default:
		log.Debug().Type("node", expr).Msg("getTableColFromInfix unhandled type")
	}
	return "", ""
}

func getTableColumnFromInfixOrLeaf(expr ast.Node) (string, string) {
	switch expr := expr.(type) {
	case ast.Infix:
		return getTableColFromInfix(expr)
	case ast.Leaf:
		if len(tables) != 1 {
			log.Debug().Type("node", expr).Msg("column is a leaf, but there's multiple tables, potentially ambiguous column name, skipping")
			return "", ""
		}
		for table := range tables {
			return table, expr.Token.Str
		}
	default:
		log.Debug().Type("node", expr).Msg("getTableColumnFromInfixOrLeaf unhandled type")
	}
	return "", ""
}

func removeAlias(s string) string {
	if s2, ok := aliases[s]; ok {
		return s2
	}
	return s
}

func getItemValue(expr ast.Node) string {
	switch expr := expr.(type) {
	case ast.Leaf:
		return expr.String()
	}
	return ""
}
