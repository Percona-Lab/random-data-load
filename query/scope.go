package query

import (
	"strings"

	"github.com/rs/zerolog/log"
	"gitlab.com/dalibo/transqlate/ast"
)

// colRef designates a column of a real table.
type colRef struct {
	Table  string
	Column string
}

// outputColumn is one column a query exposes to whatever reads from it.
//
// Columns keep their position because a CTE may rename them positionally, and
// because a set operation lines its branches up by position.
type outputColumn struct {
	// name is how the column is referenced from outside, empty when the
	// expression producing it has no name of its own.
	name string

	// refs are the real columns the value comes from, one per branch of a set
	// operation. It is empty when the value cannot be traced back to a
	// column: an expression, an aggregate, a literal.
	refs []colRef
}

// shape describes what a query exposes: its output columns, plus the sources a
// "*" hands over wholesale.
type shape struct {
	columns []outputColumn

	// passthrough holds the sources a "*" exposes. The columns of a table are
	// not known without reading its schema, so instead of enumerating them the
	// projection is deferred to the source itself.
	passthrough []*source
}

// source is anything a scope can resolve a column reference against: a base
// table, or a derived result such as a subquery or a CTE.
type source struct {
	// name is how this source is referenced inside its scope: the alias when
	// one is given, the table name otherwise.
	name string

	// base names the real table this source reads from. It is empty for a
	// derived source, and only a base source can carry a foreign key.
	base string

	// shape is what a derived source exposes, and is unset for a base table.
	shape shape

	// delegate is the source this one stands for, used when a FROM entry names
	// a CTE: resolution passes through to the CTE, which may still be having
	// its own shape worked out when this entry is registered.
	delegate *source
}

// project traces a column of this source back to the real columns it reads.
//
// An empty return means the column could not be traced to a real table, which
// is the signal to drop whatever join was being built around it.
func (s *source) project(column string) []colRef {
	switch {
	case s == nil:
		return nil
	case s.delegate != nil:
		return s.delegate.project(column)
	case s.base != "":
		return []colRef{{Table: s.base, Column: column}}
	}

	for _, out := range s.shape.columns {
		if out.name == column {
			return out.refs
		}
	}

	// Not named explicitly, so it can only come from a "*".
	for _, exposed := range s.shape.passthrough {
		if refs := exposed.project(column); len(refs) > 0 {
			return refs
		}
	}
	return nil
}

// scope is one level of name resolution: the tables, subqueries and CTEs
// visible to a single SELECT.
//
// Scopes chain to their parent so a correlated subquery reaches the names of
// the query enclosing it, while two sibling subqueries stay invisible to each
// other. Names are matched exactly as written, because an alias in an ON
// clause has to be spelled the way it was in FROM, and because quoted
// identifiers are case-sensitive on both engines.
type scope struct {
	parent  *scope
	sources map[string]*source
	ctes    map[string]*source

	// ordered lists the sources in the order they were declared, so that
	// resolving a "*" does not depend on map iteration.
	ordered []*source
}

func newScope(parent *scope) *scope {
	return &scope{
		parent:  parent,
		sources: map[string]*source{},
		ctes:    map[string]*source{},
	}
}

func (sc *scope) addSource(s *source) {
	if s.name == "" {
		return
	}
	sc.sources[s.name] = s
	sc.ordered = append(sc.ordered, s)
}

// lookup finds a source by the name used to reference it, walking outwards
// through the enclosing scopes so that correlated references resolve.
func (sc *scope) lookup(name string) *source {
	for cur := sc; cur != nil; cur = cur.parent {
		if s, ok := cur.sources[name]; ok {
			return s
		}
		if s, ok := cur.ctes[name]; ok {
			return s
		}
	}
	return nil
}

// lookupCTE finds a CTE by name, walking outwards through enclosing scopes.
func (sc *scope) lookupCTE(name string) *source {
	for cur := sc; cur != nil; cur = cur.parent {
		if s, ok := cur.ctes[name]; ok {
			return s
		}
	}
	return nil
}

// resolve turns a qualified column reference into the base columns it reads.
func (sc *scope) resolve(qualifier, column string) []colRef {
	if column == "" {
		return nil
	}

	if qualifier == "" {
		// A bare column can only be attributed when there is a single place
		// it could have come from. Doing better needs the column list of
		// every table, which the parser does not have.
		if len(sc.ordered) != 1 {
			log.Debug().Str("column", column).Int("sources", len(sc.ordered)).Msg("unqualified column with several sources in scope, cannot attribute it to a table")
			return nil
		}
		return sc.ordered[0].project(column)
	}

	if s := sc.lookup(qualifier); s != nil {
		return s.project(column)
	}

	// An unknown qualifier is taken to be a table named in full. It is what
	// happens for FROM shapes the walker does not model, such as a table
	// function, and matches how references were resolved before scopes.
	log.Debug().Str("qualifier", qualifier).Str("column", column).Msg("qualifier is not a known source, assuming it names a table")
	return []colRef{{Table: qualifier, Column: column}}
}

// analyzer walks a parsed statement and collects everything the data
// generation needs to know about it: which real tables are involved and which
// joins have to hold between them.
type analyzer struct {
	skipJoins bool

	tables map[string]struct{}
	joins  []VirtualJoin

	// aliasBases maps every alias declared in a FROM clause to the real table
	// behind it, or to the empty string when it stands for a derived result.
	//
	// It is a flat, query-wide map, which is exactly what makes it unfit for
	// resolving joins: two subqueries reusing an alias overwrite each other.
	// Only the query-parameter pass still reads it.
	aliasBases map[string]string
}

func analyze(root ast.Node, skipJoins bool) *analyzer {
	a := &analyzer{
		skipJoins:  skipJoins,
		tables:     map[string]struct{}{},
		joins:      []VirtualJoin{},
		aliasBases: map[string]string{},
	}

	// Reach the outermost SELECTs without having to know every statement
	// wrapper. Each one roots its own scope chain; from there the descent is
	// explicit, because a callback-based walk cannot tell which scope the node
	// it is looking at belongs to.
	root.Traverse(func(n ast.Node) bool {
		sel, ok := n.(ast.Select)
		if !ok {
			return true
		}
		a.analyzeSelect(sel, nil)
		return false
	})

	return a
}

func (a *analyzer) addTable(name string) {
	if name == "" {
		return
	}
	a.tables[name] = struct{}{}
}

// analyzeSelect resolves one SELECT against a scope of its own and reports the
// shape it exposes, so that a query reading from it can project through.
func (a *analyzer) analyzeSelect(sel ast.Select, parent *scope) shape {
	sc := newScope(parent)

	// ast.Select.Traverse does not walk With, so a CTE body is only ever seen
	// by descending into it explicitly, as here.
	a.registerCTEs(sc, sel.With)

	// Every source has to be registered before any condition is resolved: a
	// join may reference a table that appears later in the FROM clause.
	var conditions []ast.Node
	for _, item := range sel.From.Tables {
		a.walkFrom(sc, item.Expression, &conditions)
	}

	// Each predicate is read as one group, so that a composite key written as
	// several ANDed equalities comes out as a single foreign key over several
	// columns instead of one key per column.
	for _, condition := range conditions {
		a.collectPredicate(sc, condition)
	}
	if !sel.Where.IsZero() {
		// WHERE carries the joins of a comma-separated FROM, and the
		// correlated condition of an EXISTS subquery.
		a.collectPredicate(sc, sel.Where)
	}
	if sel.GroupBy != nil {
		// HAVING lives here.
		a.collectPredicate(sc, sel.GroupBy)
	}

	a.analyzeNestedSelects(sc, sel)

	return a.selectShape(sc, sel)
}

// selectShape reads the SELECT list to work out what the query exposes.
func (a *analyzer) selectShape(sc *scope, sel ast.Select) shape {
	var out shape

	for _, item := range sel.List {
		expr := item.Expression

		// An aliased expression is named by its alias, whatever it computes.
		if alias, ok := expr.(ast.Alias); ok {
			out.columns = append(out.columns, outputColumn{
				name: alias.Name.Str,
				refs: resolveColumnValue(sc, alias.Expression),
			})
			continue
		}

		if isStar(expr) {
			// Everything in scope is exposed under its own column names.
			out.passthrough = append(out.passthrough, sc.ordered...)
			continue
		}
		if qualifier, ok := qualifiedStar(expr); ok {
			if s := sc.lookup(qualifier); s != nil {
				out.passthrough = append(out.passthrough, s)
			}
			continue
		}

		// A plain column reference keeps its own name. Anything else holds a
		// position without being projectable.
		qualifier, column := splitColumnReference(expr)
		out.columns = append(out.columns, outputColumn{
			name: column,
			refs: sc.resolve(qualifier, column),
		})
	}

	return out
}

// walkFrom registers the sources of one FROM expression and collects the join
// conditions attached at this level. Conditions are returned rather than
// resolved so that the caller can wait until the whole clause is registered.
func (a *analyzer) walkFrom(sc *scope, expr ast.Node, conditions *[]ast.Node) {
	switch expr := expr.(type) {

	case ast.Join:
		if expr.Left != nil {
			a.walkFrom(sc, expr.Left, conditions)
		}
		if expr.Right != nil {
			a.walkFrom(sc, expr.Right, conditions)
		}
		if expr.Condition != nil {
			*conditions = append(*conditions, expr.Condition)
		}

	case ast.Alias:
		base := baseTableName(expr.Expression)
		a.aliasBases[expr.Name.Str] = base
		if base != "" {
			a.addNamedSource(sc, expr.Name.Str, base)
			return
		}
		sc.addSource(&source{
			name:  expr.Name.Str,
			shape: a.analyzeDerivedBody(sc, expr.Expression),
		})

	case ast.List:
		// A parenthesised table expression. A lone SELECT inside is a derived
		// source nothing can reference by name; anything else is a nested list
		// of table expressions.
		if len(expr.Items) == 1 {
			if sel, ok := expr.Items[0].Expression.(ast.Select); ok {
				a.analyzeSelect(sel, sc)
				return
			}
		}
		for _, item := range expr.Items {
			a.walkFrom(sc, item.Expression, conditions)
		}

	case ast.Select:
		a.analyzeSelect(expr, sc)

	case ast.IndexHints:
		a.walkFrom(sc, expr.Table, conditions)

	case ast.Leaf, ast.Infix:
		// A table or a CTE named directly, possibly qualified by its schema.
		// Its own name is how it gets referenced.
		base := baseTableName(expr)
		if base != "" {
			a.addNamedSource(sc, base, base)
			return
		}
		log.Debug().Type("node", expr).Msg("FROM item is not a table reference, ignoring")

	default:
		log.Debug().Type("node", expr).Msg("unhandled FROM item")
		// Nothing can be resolved against it, but a query nested inside it
		// still reads tables that need generating.
		a.analyzeDerivedBody(sc, expr)
	}
}

// addNamedSource registers a FROM entry that names something: a CTE when the
// name is one, a real table otherwise. Only the latter has to be generated.
func (a *analyzer) addNamedSource(sc *scope, refName, name string) {
	if cte := sc.lookupCTE(name); cte != nil {
		sc.addSource(&source{name: refName, delegate: cte})
		return
	}
	a.addTable(name)
	sc.addSource(&source{name: refName, base: name})
}

// registerCTEs resolves the CTEs a SELECT declares.
//
// They are resolved in the order they are written, so that one CTE can read
// from another declared before it.
func (a *analyzer) registerCTEs(sc *scope, with ast.With) {
	if with.IsZero() {
		return
	}

	// Every name is declared before any body is analyzed. A recursive CTE
	// refers to itself, and that reference has to read as "a CTE nothing is
	// known about yet" rather than fall through and be taken for a table that
	// would then be generated.
	for _, cte := range with.CTEs {
		sc.ctes[cte.Name.Str] = &source{name: cte.Name.Str}
	}

	for _, cte := range with.CTEs {
		if cte.Query == nil {
			continue
		}
		// A self-reference resolves to the still-empty declaration above and
		// projects to nothing, which is how the recursive branch of a
		// recursive CTE ends up contributing no foreign key.
		body := a.analyzeDerivedBody(sc, cte.Query)
		sc.ctes[cte.Name.Str].shape = applyColumnList(body, cte.Columns)
	}
}

// applyColumnList applies the column list a CTE may declare, which renames the
// body's output columns by position.
func applyColumnList(s shape, columns ast.Columns) shape {
	if columns.IsZero() {
		return s
	}

	renamed := shape{passthrough: s.passthrough}
	for i, col := range columns.Names {
		out := outputColumn{name: col.Name.Str}
		if i < len(s.columns) {
			out.refs = s.columns[i].refs
		}
		renamed.columns = append(renamed.columns, out)
	}
	return renamed
}

// analyzeDerivedBody analyzes the SELECTs a derived source is built from and
// reports the shape they expose together.
//
// The body is reached by walking rather than by unwrapping a known shape,
// because it is not always just a parenthesised query: LATERAL parses as a
// call, and a set operation puts one SELECT on each side of an operator. Each
// one becomes a query in its own right, contributing its tables and its joins,
// resolved against a scope nested in this one so a correlated reference reaches
// the enclosing names.
func (a *analyzer) analyzeDerivedBody(sc *scope, body ast.Node) shape {
	var branches []shape

	body.Traverse(func(n ast.Node) bool {
		sel, ok := n.(ast.Select)
		if !ok {
			return true
		}
		branches = append(branches, a.analyzeSelect(sel, sc))
		return false
	})

	return mergeBranches(branches)
}

// mergeBranches lines the branches of a set operation up by position, so that
// a column reading from a UNION carries one reference per branch.
func mergeBranches(branches []shape) shape {
	var merged shape

	for _, branch := range branches {
		merged.passthrough = append(merged.passthrough, branch.passthrough...)

		for i, col := range branch.columns {
			if i == len(merged.columns) {
				merged.columns = append(merged.columns, outputColumn{name: col.name})
			}
			if i >= len(merged.columns) {
				break
			}
			// The first branch names the column, the others only add where
			// their values come from.
			merged.columns[i].refs = append(merged.columns[i].refs, col.refs...)
		}
	}

	return merged
}

// analyzeNestedSelects analyzes the subqueries living outside the FROM clause:
// in the SELECT list, WHERE, GROUP BY and so on. They contribute their own
// tables and joins, and resolve against the current scope so that a correlated
// reference reaches the enclosing names.
func (a *analyzer) analyzeNestedSelects(sc *scope, sel ast.Select) {
	// Where and GroupBy are absent: collectPredicate already descended into
	// them, and analyzing their subqueries twice would double their joins.
	clauses := []ast.Node{sel.Into, sel.Hierarchy, sel.OrderBy, sel.Limit}
	if !sel.List.IsZero() {
		clauses = append(clauses, sel.List)
	}
	if !sel.Distinct.IsZero() {
		clauses = append(clauses, sel.Distinct)
	}
	if !sel.ForUpdate.IsZero() {
		clauses = append(clauses, sel.ForUpdate)
	}

	for _, clause := range clauses {
		if clause == nil {
			continue
		}
		clause.Traverse(func(n ast.Node) bool {
			nested, ok := n.(ast.Select)
			if !ok {
				return true
			}
			a.analyzeSelect(nested, sc)
			return false
		})
	}
}

// joinPair is one column-to-column equality a predicate requires.
type joinPair struct {
	left  colRef
	right colRef
}

// collectPredicate reads one predicate — an ON clause, a WHERE clause, a
// HAVING clause — for the joins it requires.
//
// Equalities ANDed together form one group, because that is how a composite key
// is written and it has to be generated as a single key over several columns:
// satisfied column by column, a child row could take each column from a
// different parent row and end up with a combination the parent never had.
//
// Equalities under an OR are kept apart. Only one of them has to hold, so
// merging them would demand more of the data than the query does.
func (a *analyzer) collectPredicate(sc *scope, predicate ast.Node) {
	var conjoined, isolated []joinPair
	a.walkPredicate(sc, predicate, &conjoined, &isolated)

	if a.skipJoins {
		return
	}

	a.emitGroupedJoins(conjoined)
	for _, pair := range isolated {
		a.emitGroupedJoins([]joinPair{pair})
	}
}

// walkPredicate follows the boolean structure of a predicate, sorting the
// equalities it finds into the ones that hold together and the ones that do
// not.
func (a *analyzer) walkPredicate(sc *scope, n ast.Node, conjoined, isolated *[]joinPair) {
	switch n := n.(type) {

	case ast.Select:
		// A subquery is a query of its own. Its conditions belong to it, not
		// to the predicate containing it.
		a.analyzeSelect(n, sc)
		return

	case ast.Where:
		// The clauses of a WHERE or ON are ANDed together, the keyword sitting
		// on the clause that follows.
		for _, clause := range n.Conditions {
			if clause.Expression == nil {
				continue
			}
			if clauseIsDisjunctive(clause) {
				a.walkPredicate(sc, clause.Expression, isolated, isolated)
				continue
			}
			a.walkPredicate(sc, clause.Expression, conjoined, isolated)
		}
		return

	case ast.Infix:
		switch {
		case n.Is("OR"):
			// Neither side can be grouped, with the other or with anything
			// outside the OR.
			a.walkPredicate(sc, n.Left, isolated, isolated)
			a.walkPredicate(sc, n.Right, isolated, isolated)
			return

		case n.Is("AND"):
			a.walkPredicate(sc, n.Left, conjoined, isolated)
			a.walkPredicate(sc, n.Right, conjoined, isolated)
			return

		case n.Is("="):
			*conjoined = append(*conjoined, a.equalityPairs(sc, n)...)
			// An operand may still hold a scalar subquery.
			a.analyzeSelectsIn(sc, n.Left)
			a.analyzeSelectsIn(sc, n.Right)
			return

		case n.Is("IN"):
			*conjoined = append(*conjoined, a.semiJoinPairs(sc, n)...)
			return

		case n.Is("NOT", "IN"):
			// NOT IN asks for the values to stay apart, which is the opposite
			// of what a foreign key would give it. The subquery still reads
			// tables that need generating.
			a.analyzeSelectsIn(sc, n.Left)
			a.analyzeSelectsIn(sc, n.Right)
			return
		}

	case ast.Prefix:
		if n.Token.Str == "NOT" {
			// Negation inverts what the predicate requires, so nothing under
			// it describes a foreign key.
			a.analyzeSelectsIn(sc, n.Expression)
			return
		}
	}

	// Anything else — BETWEEN, IS NULL, a function call, a clause wrapper —
	// is not a join itself but may contain one, or a nested query.
	self := true
	n.Traverse(func(child ast.Node) bool {
		if self {
			self = false
			return true
		}
		a.walkPredicate(sc, child, conjoined, isolated)
		return false
	})
}

// clauseIsDisjunctive reports whether a clause is ORed with the one before it.
func clauseIsDisjunctive(clause ast.Clause) bool {
	for _, keyword := range clause.Keywords {
		if keyword.Str == "OR" {
			return true
		}
	}
	return false
}

// equalityPairs reads a column-to-column equality as a foreign key.
func (a *analyzer) equalityPairs(sc *scope, infix ast.Infix) []joinPair {
	leftQualifier, leftColumn := splitColumnReference(infix.Left)
	rightQualifier, rightColumn := splitColumnReference(infix.Right)

	if leftColumn == "" || rightColumn == "" {
		// A column compared against a literal or an expression is a
		// restriction on that column, not a link between two of them. The
		// literal is picked up as a query parameter elsewhere.
		log.Debug().Type("left", infix.Left).Type("right", infix.Right).Msg("equality is not between two column references, not a join")
		return nil
	}

	left := sc.resolve(leftQualifier, leftColumn)
	right := sc.resolve(rightQualifier, rightColumn)
	if len(left) == 0 || len(right) == 0 {
		a.warnUntraceable(infix, left, right)
		return nil
	}
	return crossPairs(left, right)
}

// semiJoinPairs reads an IN (subquery) as a foreign key.
//
// A semi-join does not multiply rows the way a join does, but it asks the same
// thing of the data: every value on the left has to exist among the ones the
// subquery returns, or the query matches nothing.
func (a *analyzer) semiJoinPairs(sc *scope, infix ast.Infix) []joinPair {
	// A subquery can sit on either side; the left one carries no values to
	// match against, so it is only analyzed for the tables it reads.
	a.analyzeSelectsIn(sc, infix.Left)

	inner, ok := a.analyzeOutermostSelect(sc, infix.Right)
	if !ok {
		// A literal list rather than a subquery. Those become query
		// parameters, not joins.
		return nil
	}

	leftQualifier, leftColumn := splitColumnReference(infix.Left)
	if leftColumn == "" {
		log.Debug().Type("left", infix.Left).Msg("left of IN is not a column reference, not a semi-join")
		return nil
	}

	left := sc.resolve(leftQualifier, leftColumn)
	right := firstProjectedColumn(inner)
	if len(left) == 0 || len(right) == 0 {
		a.warnUntraceable(infix, left, right)
		return nil
	}

	// The subquery side is the parent: the values being tested have to exist
	// among the ones it returns, which makes the tested column the child.
	// Foreign keys are recorded parent first.
	return crossPairs(right, left)
}

// warnUntraceable reports a condition the query requires but the generator
// cannot honour, because a column could not be followed back to a real table.
func (a *analyzer) warnUntraceable(infix ast.Infix, left, right []colRef) {
	log.Warn().
		Str("condition", nodeText(infix)).
		Bool("left resolved", len(left) > 0).
		Bool("right resolved", len(right) > 0).
		Msg("cannot trace both sides of this condition down to real columns, no foreign key will be generated for it")
}

// crossPairs pairs every column on one side with every column on the other.
//
// A side reading from a set operation carries one reference per branch, and the
// condition has to hold against each of them.
func crossPairs(left, right []colRef) []joinPair {
	pairs := make([]joinPair, 0, len(left)*len(right))
	for _, l := range left {
		for _, r := range right {
			pairs = append(pairs, joinPair{left: l, right: r})
		}
	}
	return pairs
}

// firstProjectedColumn reports where the single column of a subquery comes
// from. An IN subquery returns exactly one column.
func firstProjectedColumn(sh shape) []colRef {
	if len(sh.columns) == 0 {
		return nil
	}
	return sh.columns[0].refs
}

// analyzeOutermostSelect analyzes the query wrapped in an expression, without
// descending into the subqueries nested inside it, and reports what it exposes.
func (a *analyzer) analyzeOutermostSelect(sc *scope, expr ast.Node) (shape, bool) {
	var (
		result shape
		found  bool
	)
	expr.Traverse(func(n ast.Node) bool {
		if found {
			return false
		}
		sel, ok := n.(ast.Select)
		if !ok {
			return true
		}
		result, found = a.analyzeSelect(sel, sc), true
		return false
	})
	return result, found
}

// analyzeSelectsIn analyzes every query nested in an expression, for the tables
// they read.
func (a *analyzer) analyzeSelectsIn(sc *scope, expr ast.Node) {
	expr.Traverse(func(n ast.Node) bool {
		sel, ok := n.(ast.Select)
		if !ok {
			return true
		}
		a.analyzeSelect(sel, sc)
		return false
	})
}

// emitGroupedJoins turns the equalities of one predicate into foreign keys,
// one per pair of tables, so that a composite key stays a single key.
func (a *analyzer) emitGroupedJoins(pairs []joinPair) {
	type tablePair struct{ left, right string }

	var order []tablePair
	groups := map[tablePair]*VirtualJoin{}
	seen := map[joinPair]bool{}

	for _, pair := range pairs {
		if seen[pair] {
			// The same equality written twice asks for nothing more.
			continue
		}
		seen[pair] = true

		left, right := pair.left, pair.right
		key := tablePair{left.Table, right.Table}

		// The two tables may be named in either order from one equality to
		// the next; the group's own orientation wins so that the column lists
		// stay lined up.
		if _, ok := groups[key]; !ok {
			flipped := tablePair{right.Table, left.Table}
			if _, ok := groups[flipped]; ok {
				key = flipped
				left, right = right, left
			}
		}

		group, ok := groups[key]
		if !ok {
			group = &VirtualJoin{
				Left:  VirtualJoinPart{Table: left.Table},
				Right: VirtualJoinPart{Table: right.Table},
			}
			groups[key] = group
			order = append(order, key)
		}

		group.Left.Columns = append(group.Left.Columns, left.Column)
		group.Right.Columns = append(group.Right.Columns, right.Column)
	}

	for _, key := range order {
		group := groups[key]
		log.Debug().
			Str("left", group.Left.Table+"("+strings.Join(group.Left.Columns, ",")+")").
			Str("right", group.Right.Table+"("+strings.Join(group.Right.Columns, ",")+")").
			Msg("join detected")

		a.joins = append(a.joins, *group)
	}
}

// resolveColumnValue resolves an expression appearing where a value is
// expected, reporting the real columns it reads when it is a plain column
// reference and nothing otherwise.
func resolveColumnValue(sc *scope, expr ast.Node) []colRef {
	qualifier, column := splitColumnReference(expr)
	if column == "" {
		return nil
	}
	return sc.resolve(qualifier, column)
}

// splitColumnReference splits a column reference into the name qualifying it
// and the column itself. The qualifier is empty for a bare column, and both
// are empty when the expression is not a column reference at all.
//
// The reference may carry any number of qualifiers: t.c, schema.t.c and
// db.schema.t.c all name a column of t.
func splitColumnReference(expr ast.Node) (qualifier, column string) {
	parts := dottedParts(expr)
	switch len(parts) {
	case 0:
		return "", ""
	case 1:
		return "", parts[0]
	default:
		return parts[len(parts)-2], parts[len(parts)-1]
	}
}

// dottedParts flattens a dotted name into its parts, returning nil when the
// expression is not a name at all. Literals are rejected, so a condition
// comparing a column to a constant is not mistaken for a join.
func dottedParts(expr ast.Node) []string {
	switch expr := expr.(type) {

	case ast.Leaf:
		if !expr.IsIdentifier() {
			return nil
		}
		return []string{expr.Token.Str}

	case ast.Infix:
		if !expr.Is(".") {
			return nil
		}
		left := dottedParts(expr.Left)
		right := dottedParts(expr.Right)
		if left == nil || right == nil {
			return nil
		}
		return append(left, right...)
	}
	return nil
}

// baseTableName returns the real table a FROM expression reads from, or the
// empty string when the expression is derived and has no table of its own.
func baseTableName(expr ast.Node) string {
	switch expr := expr.(type) {

	case ast.Alias: // orders AS o
		return baseTableName(expr.Expression)

	case ast.Leaf: // orders
		if !expr.IsIdentifier() {
			return ""
		}
		return expr.Token.Str

	case ast.Infix: // public.orders
		if expr.Is(".") {
			return baseTableName(expr.Right)
		}

	case ast.IndexHints: // orders o USE INDEX (...)
		return baseTableName(expr.Table)
	}

	// A subquery, a table function, a VALUES list: no table to point at.
	return ""
}

// isStar reports whether an expression is a bare "*".
func isStar(expr ast.Node) bool {
	leaf, ok := expr.(ast.Leaf)
	return ok && leaf.Token.Str == "*"
}

// qualifiedStar reports the source named by a "t.*" expression.
func qualifiedStar(expr ast.Node) (string, bool) {
	infix, ok := expr.(ast.Infix)
	if !ok || !infix.Is(".") || !isStar(infix.Right) {
		return "", false
	}
	parts := dottedParts(infix.Left)
	if len(parts) == 0 {
		return "", false
	}
	return parts[len(parts)-1], true
}

// nodeText renders a node back to something close to how it was written, for
// log messages that have to point at a specific piece of the query.
func nodeText(n ast.Node) string {
	var b strings.Builder
	for _, t := range n.Tokens() {
		b.WriteString(t.Prefix)
		b.WriteString(t.Raw)
		b.WriteString(t.Suffix)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
