package query

import (
	"strings"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"gitlab.com/dalibo/transqlate/ast"
	"gitlab.com/dalibo/transqlate/lexer"
	"gitlab.com/dalibo/transqlate/mysql"
	"gitlab.com/dalibo/transqlate/parser"
	"gitlab.com/dalibo/transqlate/rewrite"
)

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

	analyzed := analyze(parsed, skipJoins)
	identifiers := traverseIdentifiers(parsed)
	queryParams := traverseQueryParameters(parsed, analyzed.tables, analyzed.aliasBases)

	return analyzed.tables, identifiers, analyzed.joins, queryParams, nil
}

func traverseIdentifiers(n ast.Node) map[string]struct{} {
	identifiers := map[string]struct{}{}

	emptyMap := false

	// don't need to iterate over selected columns, joins, where, group bys
	// having every raw identifiers will be good enough since it's used as a whitelist
	// it might have collisions down the line, but at worst it would only generate data on some extra column
	var traverser ast.Traverser
	traverser = func(n ast.Node) bool {
		switch n := n.(type) {
		case ast.Select:
			// ast.Select.Traverse skips With, so a column a CTE reads would
			// otherwise be left out of the whitelist and never generated,
			// even though the CTE filters on it.
			if !n.With.IsZero() {
				n.With.Traverse(traverser)
			}
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

// traverseQueryParameters collects the literals a query compares columns to,
// so that some of the generated rows can carry them and the query matches
// something.
//
// It resolves column references against the whole query at once, using the
// flat alias map and the complete table list rather than the scope the
// reference sits in. A literal inside a subquery is therefore attributed to a
// table only when the query as a whole reads a single one.
func traverseQueryParameters(n ast.Node, tables map[string]struct{}, aliasBases map[string]string) map[string][]string {

	queryParams := map[string][]string{}

	traverser := func(n ast.Node) bool {
		switch n := n.(type) {
		case ast.Infix:
			switch {
			case n.Is("="):
				right, ok := n.Right.(ast.Leaf)
				if !ok {
					break
				}
				record(queryParams, n.Left, []string{right.String()}, tables, aliasBases)
			case n.Is("IN"):
				right, ok := n.Right.(ast.List)
				if !ok {
					break
				}
				values := []string{}
				for _, item := range right.Items {
					if val := getItemValue(item.Expression); val != "" {
						values = append(values, val)
					}
				}
				record(queryParams, n.Left, values, tables, aliasBases)
			}
		}
		return true
	}

	n.Traverse(traverser)
	return queryParams
}

// record files literals under the column they constrain.
//
// A column that cannot be attributed to a table is dropped: the generator has
// nowhere to put the values, and filing them under a malformed name only leaves
// an entry nothing will ever read.
func record(queryParams map[string][]string, expr ast.Node, values []string, tables map[string]struct{}, aliasBases map[string]string) {
	if len(values) == 0 {
		// An IN whose operand is a subquery rather than a list of literals.
		// The overlap it requires is handled as a semi-join instead.
		return
	}

	table, column := queryWideColumn(expr, tables, aliasBases)
	if table == "" || column == "" {
		log.Debug().Str("values", strings.Join(values, ",")).Msg("cannot attribute these query parameters to a column, dropping them")
		return
	}
	queryParams[table+"."+column] = append(queryParams[table+"."+column], values...)
}

// queryWideColumn resolves a column reference without regard for scope.
//
// A qualified reference is looked up in the flat alias map, falling back to the
// qualifier itself as a table name. A bare column can only be attributed when
// the query reads exactly one table.
func queryWideColumn(expr ast.Node, tables map[string]struct{}, aliasBases map[string]string) (string, string) {
	qualifier, column := splitColumnReference(expr)
	if column == "" {
		log.Debug().Type("node", expr).Msg("not a column reference")
		return "", ""
	}

	if qualifier == "" {
		if len(tables) != 1 {
			log.Debug().Type("node", expr).Msg("column is a leaf, but there's multiple tables, potentially ambiguous column name, skipping")
			return "", ""
		}
		for table := range tables {
			return table, column
		}
	}

	if base, ok := aliasBases[qualifier]; ok {
		qualifier = base
	}
	if qualifier == "" {
		return "", ""
	}
	return qualifier, column
}

func getItemValue(expr ast.Node) string {
	switch expr := expr.(type) {
	case ast.Leaf:
		return expr.String()
	}
	return ""
}
