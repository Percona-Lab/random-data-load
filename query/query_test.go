package query

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

// These tests pin the behaviour of ParseQuery so the scope refactor can be
// proven not to change it.
//
// Cases are split in two groups:
//   - "supported" shapes, which produce the joins we expect;
//   - "known gaps", which record what the parser does today even though it is
//     wrong. Their `gap` field names the shortcoming. When a gap is fixed, the
//     case moves to the supported group and its expectations change in the same
//     commit, so the diff shows exactly which queries started behaving
//     differently.
//
// Every case is engine-parameterised: a shape that parses on one engine only is
// declared with a single engine.

func TestMain(m *testing.M) {
	// ParseQuery logs its decisions at debug level, which drowns test output.
	zerolog.SetGlobalLevel(zerolog.Disabled)
	os.Exit(m.Run())
}

type parseCase struct {
	name    string
	engines []string
	query   string

	// gap, when set, marks the expectations below as current-but-wrong and
	// describes what is missing.
	gap string

	skipJoins bool

	wantTables      []string
	wantJoins       []string // canonical form, see formatJoin
	wantIdentifiers []string
	wantParams      map[string][]string
}

var bothEngines = []string{"pg", "mysql"}

func TestParseQuery(t *testing.T) {
	cases := []parseCase{
		// ---------------------------------------------------------------
		// Supported shapes
		// ---------------------------------------------------------------
		{
			name:    "single_table_no_join",
			engines: bothEngines,
			query:   "select c1, c2 from t1 where c1 = 'x';",

			wantTables:      []string{"t1"},
			wantJoins:       nil,
			wantIdentifiers: []string{"c1", "c2", "t1"},
			wantParams:      map[string][]string{"t1.c1": {"x"}},
		},
		{
			name:    "flat_join_chain",
			engines: bothEngines,
			query: "select sum(p.price) from orders o " +
				"join order_items oi on o.order_id = oi.order_id " +
				"join products p on p.id = oi.product_no " +
				"where o.currency = 'EUR';",

			wantTables: []string{"order_items", "orders", "products"},
			wantJoins: []string{
				"orders(order_id)=order_items(order_id)",
				"products(id)=order_items(product_no)",
			},
			wantIdentifiers: []string{
				"currency", "id", "o", "oi", "order_id", "order_items",
				"orders", "p", "price", "product_no", "products", "sum",
			},
			wantParams: map[string][]string{"orders.currency": {"EUR"}},
		},
		{
			name:    "join_nested_inside_derived_table",
			engines: bothEngines,
			query: "select x.zip from (" +
				"select o.zip, oi.product_no from orders o join order_items oi on o.order_id = oi.order_id" +
				") x;",

			// The derived table's own join resolves because both sides are
			// base tables inside that subquery. Nesting depth alone is not a
			// limitation.
			wantTables: []string{"order_items", "orders"},
			wantJoins:  []string{"orders(order_id)=order_items(order_id)"},
			wantIdentifiers: []string{
				"o", "oi", "order_id", "order_items", "orders",
				"product_no", "x", "zip",
			},
			wantParams: map[string][]string{},
		},
		{
			name:      "skip_joins_flag_disables_join_detection",
			engines:   bothEngines,
			query:     "select 1 from orders o join order_items oi on o.order_id = oi.order_id;",
			skipJoins: true,

			wantTables:      []string{"order_items", "orders"},
			wantJoins:       nil,
			wantIdentifiers: []string{"o", "oi", "order_id", "order_items", "orders"},
			wantParams:      map[string][]string{},
		},
		{
			name:    "star_cancels_identifier_whitelist",
			engines: bothEngines,
			query:   "select * from orders o join order_items oi on o.order_id = oi.order_id;",

			// A '*' anywhere empties the whitelist so every column is generated.
			wantTables:      []string{"order_items", "orders"},
			wantJoins:       []string{"orders(order_id)=order_items(order_id)"},
			wantIdentifiers: []string{},
			wantParams:      map[string][]string{},
		},
		{
			name:    "query_params_in_list",
			engines: bothEngines,
			query:   "select zip from orders where currency in ('EUR', 'USD');",

			wantTables:      []string{"orders"},
			wantJoins:       nil,
			wantIdentifiers: []string{"currency", "orders", "zip"},
			wantParams:      map[string][]string{"orders.currency": {"EUR", "USD"}},
		},

		{
			// Quoted identifiers keep their case, and the case used in the ON
			// clause has to match the case used in FROM for the join to
			// resolve. Guards the fk_virtual_mixed_cases integration test.
			name:    "quoted_identifiers_mysql",
			engines: []string{"mysql"},
			query: "select * from `PARENT_TABLE` pT join `CHILD_TABLE` cT " +
				"on pT.`ParentTableId` = cT.`ParentTableId` " +
				"where pT.`pARENTTableData` is not null;",

			wantTables:      []string{"CHILD_TABLE", "PARENT_TABLE"},
			wantJoins:       []string{"PARENT_TABLE(ParentTableId)=CHILD_TABLE(ParentTableId)"},
			wantIdentifiers: []string{},
			wantParams:      map[string][]string{},
		},
		{
			name:    "quoted_identifiers_pg",
			engines: []string{"pg"},
			query:   `select 1 from "Orders" o join order_items oi on o."OrderId" = oi.order_id;`,

			wantTables:      []string{"Orders", "order_items"},
			wantJoins:       []string{"Orders(OrderId)=order_items(order_id)"},
			wantIdentifiers: []string{"OrderId", "Orders", "o", "oi", "order_id", "order_items"},
			wantParams:      map[string][]string{},
		},

		{
			// A three-part name used to crash: the column reference parser
			// asserted that the left side of the dot was a bare leaf.
			name:    "schema_qualified_column_in_on",
			engines: bothEngines,
			query: "select 1 from public.orders o join order_items oi " +
				"on public.orders.order_id = oi.order_id;",

			wantTables: []string{"order_items", "orders"},
			wantJoins:  []string{"orders(order_id)=order_items(order_id)"},
			// The alias "o" is declared but never referenced, and identifiers
			// are collected from references: ast.Alias.Traverse does not visit
			// the name it binds. "public" is collected as though it were a
			// column, which only ever widens the whitelist.
			wantIdentifiers: []string{"oi", "order_id", "order_items", "orders", "public"},
			wantParams:      map[string][]string{},
		},

		{
			// LATERAL parses as a call rather than a parenthesised query, so
			// the body has to be reached by walking. The inner ON also
			// references the outer alias "o", which only resolves by looking
			// outwards through the enclosing scope.
			name:    "lateral_derived_table_correlated_on",
			engines: bothEngines,
			query: "select 1 from orders o join lateral (" +
				"select oi.order_id from order_items oi " +
				"join products p on p.id = oi.product_no and oi.order_id = o.order_id" +
				") x on true;",

			wantTables: []string{"order_items", "orders", "products"},
			wantJoins: []string{
				"order_items(order_id)=orders(order_id)",
				"products(id)=order_items(product_no)",
			},
			wantIdentifiers: []string{
				"id", "lateral", "o", "oi", "order_id", "order_items",
				"orders", "p", "product_no", "products",
			},
			wantParams: map[string][]string{},
		},

		{
			// The join is projected through the derived table down onto the
			// real column it selects.
			name:    "derived_table_joined_at_outer_level",
			engines: bothEngines,
			query: "select count(*) from (select order_id from orders where currency = 'EUR') r " +
				"join order_items oi on r.order_id = oi.order_id;",

			wantTables: []string{"order_items", "orders"},
			wantJoins:  []string{"orders(order_id)=order_items(order_id)"},
			// count(*) empties the whitelist.
			wantIdentifiers: []string{},
			// The query-parameter pass still resolves query-wide, so the bare
			// "currency" stays ambiguous with two tables in play. It is now
			// dropped rather than filed under a malformed name.
			wantParams: map[string][]string{},
		},
		{
			// A derived table renaming its column: the join has to follow the
			// alias back to the column underneath.
			name:    "derived_table_renamed_column",
			engines: bothEngines,
			query: "select 1 from (select o.order_id as oid from orders o) r " +
				"join order_items oi on r.oid = oi.order_id;",

			wantTables: []string{"order_items", "orders"},
			wantJoins:  []string{"orders(order_id)=order_items(order_id)"},
			wantIdentifiers: []string{
				"o", "oi", "oid", "order_id", "order_items", "orders", "r",
			},
			wantParams: map[string][]string{},
		},
		{
			// "select *" exposes the columns of whatever it reads without the
			// parser knowing their names, so projection is deferred to the
			// source itself.
			name:    "derived_table_select_star",
			engines: bothEngines,
			query: "select 1 from (select * from orders) r " +
				"join order_items oi on r.order_id = oi.order_id;",

			wantTables:      []string{"order_items", "orders"},
			wantJoins:       []string{"orders(order_id)=order_items(order_id)"},
			wantIdentifiers: []string{},
			wantParams:      map[string][]string{},
		},
		{
			name:    "derived_table_qualified_star",
			engines: bothEngines,
			query: "select 1 from (select o.* from orders o join products p on p.id = o.zip) r " +
				"join order_items oi on r.order_id = oi.order_id;",

			wantTables: []string{"order_items", "orders", "products"},
			wantJoins: []string{
				"orders(order_id)=order_items(order_id)",
				"products(id)=orders(zip)",
			},
			wantIdentifiers: []string{},
			wantParams:      map[string][]string{},
		},
		{
			// Two levels of derived table: the reference is projected down
			// through both.
			name:    "derived_table_nested_twice",
			engines: bothEngines,
			query: "select 1 from (select inner1.order_id from (select order_id from orders) inner1) r " +
				"join order_items oi on r.order_id = oi.order_id;",

			wantTables: []string{"order_items", "orders"},
			wantJoins:  []string{"orders(order_id)=order_items(order_id)"},
			wantIdentifiers: []string{
				"inner1", "oi", "order_id", "order_items", "orders", "r",
			},
			wantParams: map[string][]string{},
		},

		{
			// Either equality can hold on its own, so merging them into a
			// composite key would demand more of the data than the query does.
			name:    "or_ed_equalities_stay_separate",
			engines: bothEngines,
			query: "select 1 from orders o, order_items oi " +
				"where o.order_id = oi.order_id or o.zip = oi.product_no;",

			wantTables: []string{"order_items", "orders"},
			wantJoins: []string{
				"orders(order_id)=order_items(order_id)",
				"orders(zip)=order_items(product_no)",
			},
			wantIdentifiers: []string{
				"o", "oi", "order_id", "order_items", "orders",
				"product_no", "zip",
			},
			wantParams: map[string][]string{},
		},
		{
			// An AND chain under an OR is not merged either: the conservative
			// reading never over-constrains the data.
			name:    "and_chain_under_or_not_merged",
			engines: bothEngines,
			query:   "select 1 from a, b where a.x = b.y and a.z = b.w or a.p = b.q;",

			wantTables: []string{"a", "b"},
			wantJoins: []string{
				"a(p)=b(q)",
				"a(x)=b(y)",
				"a(z)=b(w)",
			},
			wantIdentifiers: []string{"a", "b", "p", "q", "w", "x", "y", "z"},
			wantParams:      map[string][]string{},
		},
		{
			// The two tables are named in the opposite order in the second
			// equality; the group's orientation has to win so the column
			// lists stay lined up.
			name:    "composite_condition_orientation_flipped",
			engines: bothEngines,
			query: "select 1 from purchases p join items i " +
				"on p.id = i.purchase_id and i.created_at = p.created_at;",

			wantTables: []string{"items", "purchases"},
			wantJoins:  []string{"purchases(id,created_at)=items(purchase_id,created_at)"},
			wantIdentifiers: []string{
				"created_at", "i", "id", "items", "p", "purchase_id", "purchases",
			},
			wantParams: map[string][]string{},
		},
		{
			name:    "composite_comma_join_in_where",
			engines: bothEngines,
			query: "select 1 from purchases p, items i " +
				"where p.id = i.purchase_id and p.created_at = i.created_at;",

			wantTables: []string{"items", "purchases"},
			wantJoins:  []string{"purchases(id,created_at)=items(purchase_id,created_at)"},
			wantIdentifiers: []string{
				"created_at", "i", "id", "items", "p", "purchase_id", "purchases",
			},
			wantParams: map[string][]string{},
		},
		{
			name:    "duplicate_equality_yields_one_column",
			engines: bothEngines,
			query: "select 1 from purchases p join items i " +
				"on p.id = i.purchase_id and p.id = i.purchase_id;",

			wantTables:      []string{"items", "purchases"},
			wantJoins:       []string{"purchases(id)=items(purchase_id)"},
			wantIdentifiers: []string{"i", "id", "items", "p", "purchase_id", "purchases"},
			wantParams:      map[string][]string{},
		},
		{
			// One ON clause spanning two different pairs of tables: each pair
			// is its own key.
			name:    "one_on_clause_two_table_pairs",
			engines: bothEngines,
			query:   "select 1 from a join b on a.x = b.y and a.z = c.w join c on c.k = a.m;",

			wantTables: []string{"a", "b", "c"},
			wantJoins: []string{
				"a(x)=b(y)",
				"a(z)=c(w)",
				"c(k)=a(m)",
			},
			wantIdentifiers: []string{"a", "b", "c", "k", "m", "w", "x", "y", "z"},
			wantParams:      map[string][]string{},
		},
		{
			// A semi-join reaching a real table through a CTE.
			name:    "semijoin_through_cte",
			engines: bothEngines,
			query: "with a as (select order_id from orders) " +
				"select 1 from order_items oi where oi.order_id in (select a.order_id from a);",

			wantTables:      []string{"order_items", "orders"},
			wantJoins:       []string{"orders(order_id)=order_items(order_id)"},
			wantIdentifiers: []string{"a", "oi", "order_id", "order_items", "orders"},
			wantParams:      map[string][]string{},
		},

		// ---------------------------------------------------------------
		// Deliberately not turned into foreign keys
		// ---------------------------------------------------------------
		{
			// NOT IN asks for the values to stay apart. A foreign key would
			// force the opposite, so none is generated -- but the subquery's
			// table is still needed.
			name:    "not_in_is_not_a_semijoin",
			engines: bothEngines,
			query: "select o.zip from orders o " +
				"where o.order_id not in (select oi.order_id from order_items oi);",

			wantTables:      []string{"order_items", "orders"},
			wantJoins:       nil,
			wantIdentifiers: []string{"o", "oi", "order_id", "order_items", "orders", "zip"},
			wantParams:      map[string][]string{},
		},
		{
			name:    "negated_equality_is_not_a_join",
			engines: bothEngines,
			query:   "select 1 from a, b where not (a.x = b.y);",

			wantTables:      []string{"a", "b"},
			wantJoins:       nil,
			wantIdentifiers: []string{"a", "b", "x", "y"},
			wantParams:      map[string][]string{},
		},
		{
			// A scalar subquery is not a column, so there is nothing to tie
			// together, but its table still has to be generated.
			name:    "scalar_subquery_operand",
			engines: bothEngines,
			query: "select 1 from orders o " +
				"where o.order_id = (select max(oi.order_id) from order_items oi);",

			wantTables: []string{"order_items", "orders"},
			wantJoins:  nil,
			wantIdentifiers: []string{
				"max", "o", "oi", "order_id", "order_items", "orders",
			},
			wantParams: map[string][]string{},
		},

		// ---------------------------------------------------------------
		// Known gaps
		// ---------------------------------------------------------------
		{
			name:    "using_clause_not_supported",
			engines: bothEngines,
			gap: "USING parses as a prefix holding only column names, so the two " +
				"sides of the join have to be threaded through to it; for a " +
				"chained join, which of the left-hand tables owns the column " +
				"cannot be known without reading the schema",
			query: "select 1 from a join b using (x);",

			wantTables:      []string{"a", "b"},
			wantJoins:       nil, // want: a(x)=b(x)
			wantIdentifiers: []string{"a", "b", "x"},
			wantParams:      map[string][]string{},
		},
		{
			name:    "ambiguous_unqualified_join_columns",
			engines: bothEngines,
			gap: "attributing a bare column to one of several tables needs their " +
				"column lists, which the parser does not have",
			query: "select 1 from x join y on apple = pear;",

			wantTables:      []string{"x", "y"},
			wantJoins:       nil,
			wantIdentifiers: []string{"apple", "pear", "x", "y"},
			wantParams:      map[string][]string{},
		},
		{
			name:    "derived_table_aggregated_column",
			engines: bothEngines,
			gap: "an aggregate cannot be traced to a column, so the join is " +
				"dropped with a warning rather than guessed at",
			query: "select 1 from (select sum(price) as total from products) r " +
				"join order_items oi on r.total = oi.order_id;",

			wantTables:      []string{"order_items", "products"},
			wantJoins:       nil,
			wantIdentifiers: []string{"oi", "order_id", "order_items", "price", "products", "r", "sum", "total"},
			wantParams:      map[string][]string{},
		},
		{
			// The CTE name is not a table: the join is projected onto the
			// table its body reads, and only that table is generated.
			name:    "cte_simple",
			engines: bothEngines,
			query: "with recent as (select order_id, currency from orders where currency = 'EUR') " +
				"select count(*) from recent r join order_items oi on r.order_id = oi.order_id;",

			wantTables:      []string{"order_items", "orders"},
			wantJoins:       []string{"orders(order_id)=order_items(order_id)"},
			wantIdentifiers: []string{},
			wantParams:      map[string][]string{},
		},
		{
			name:    "cte_two_declared",
			engines: bothEngines,
			query: "with a as (select order_id from orders), " +
				"b as (select order_id, product_no from order_items) " +
				"select 1 from a join b on a.order_id = b.order_id;",

			wantTables: []string{"order_items", "orders"},
			wantJoins:  []string{"orders(order_id)=order_items(order_id)"},
			// The CTE bodies now contribute their columns to the whitelist,
			// which is what makes them get generated at all.
			wantIdentifiers: []string{
				"a", "b", "order_id", "order_items", "orders", "product_no",
			},
			wantParams: map[string][]string{},
		},
		{
			// A CTE reading from an earlier one: the reference is projected
			// down through both.
			name:    "cte_reading_from_earlier_cte",
			engines: bothEngines,
			query: "with a as (select order_id from orders), b as (select a.order_id from a) " +
				"select 1 from b join order_items oi on b.order_id = oi.order_id;",

			wantTables: []string{"order_items", "orders"},
			wantJoins:  []string{"orders(order_id)=order_items(order_id)"},
			wantIdentifiers: []string{
				"a", "b", "oi", "order_id", "order_items", "orders",
			},
			wantParams: map[string][]string{},
		},
		{
			// A CTE declared inside a subquery stays confined to it, and is
			// still reachable from a derived table nested deeper.
			name:    "cte_visible_inside_derived_table",
			engines: bothEngines,
			query: "with a as (select order_id from orders) " +
				"select 1 from (select a.order_id from a) d " +
				"join order_items oi on d.order_id = oi.order_id;",

			wantTables: []string{"order_items", "orders"},
			wantJoins:  []string{"orders(order_id)=order_items(order_id)"},
			wantIdentifiers: []string{
				"a", "d", "oi", "order_id", "order_items", "orders",
			},
			wantParams: map[string][]string{},
		},
		{
			// The declared column list renames the body's columns by
			// position, so "x" has to be followed back to order_id.
			name:    "cte_with_explicit_column_list",
			engines: bothEngines,
			query: "with a(x) as (select order_id from orders) " +
				"select 1 from a join order_items oi on a.x = oi.order_id;",

			wantTables: []string{"order_items", "orders"},
			wantJoins:  []string{"orders(order_id)=order_items(order_id)"},
			wantIdentifiers: []string{
				"a", "oi", "order_id", "order_items", "orders", "x",
			},
			wantParams: map[string][]string{},
		},
		{
			// Each branch of the union is a distinct place the value can come
			// from, so each one gets its own foreign key.
			name:    "cte_with_union_branches",
			engines: bothEngines,
			query: "with a as (select order_id from orders union all select order_id from order_items) " +
				"select 1 from a join products p on p.id = a.order_id;",

			wantTables: []string{"order_items", "orders", "products"},
			wantJoins: []string{
				"products(id)=order_items(order_id)",
				"products(id)=orders(order_id)",
			},
			wantIdentifiers: []string{
				"a", "id", "order_id", "order_items", "orders", "p", "products",
			},
			wantParams: map[string][]string{},
		},
		{
			// The anchor branch resolves; the self-reference contributes
			// nothing rather than being mistaken for a table to generate.
			name:    "cte_recursive",
			engines: bothEngines,
			query: "with recursive t as (select order_id from orders union all select order_id from t) " +
				"select 1 from t;",

			wantTables:      []string{"orders"},
			wantJoins:       nil,
			wantIdentifiers: []string{"order_id", "orders", "t"},
			wantParams:      map[string][]string{},
		},
		{
			// A comma-separated FROM puts the join in WHERE, which is read as
			// a predicate like any ON clause.
			name:    "comma_join_condition_in_where",
			engines: bothEngines,
			query:   "select 1 from orders o, order_items oi where o.order_id = oi.order_id;",

			wantTables:      []string{"order_items", "orders"},
			wantJoins:       []string{"orders(order_id)=order_items(order_id)"},
			wantIdentifiers: []string{"o", "oi", "order_id", "order_items", "orders"},
			wantParams:      map[string][]string{},
		},
		{
			// A semi-join does not multiply rows, but it demands the same
			// value overlap as a join: without it the query matches nothing.
			name:    "semijoin_in_subquery",
			engines: bothEngines,
			query:   "select o.zip from orders o where o.order_id in (select oi.order_id from order_items oi);",

			// Recorded parent first: orders is the child, since its values
			// have to exist among the ones the subquery returns.
			wantTables: []string{"order_items", "orders"},
			wantJoins:  []string{"order_items(order_id)=orders(order_id)"},
			wantIdentifiers: []string{
				"o", "oi", "order_id", "order_items", "orders", "zip",
			},
			// The IN operand is a subquery, so it yields a join rather than
			// literals to inject.
			wantParams: map[string][]string{},
		},
		{
			// The correlated condition sits in the subquery's own WHERE and
			// reaches "o" by looking outwards through the enclosing scope.
			name:    "exists_correlated",
			engines: bothEngines,
			query: "select o.zip from orders o where exists " +
				"(select 1 from order_items oi where oi.order_id = o.order_id);",

			wantTables: []string{"order_items", "orders"},
			wantJoins:  []string{"order_items(order_id)=orders(order_id)"},
			wantIdentifiers: []string{
				"o", "oi", "order_id", "order_items", "orders", "zip",
			},
			wantParams: map[string][]string{},
		},
		{
			// Only equality implies that the values have to match. A range
			// condition does not, and no longer invents a foreign key.
			name:    "non_equality_join_operator",
			engines: bothEngines,
			query:   "select 1 from purchases p join items i on p.id > i.purchase_id;",

			wantTables:      []string{"items", "purchases"},
			wantJoins:       nil,
			wantIdentifiers: []string{"i", "id", "items", "p", "purchase_id", "purchases"},
			wantParams:      map[string][]string{},
		},
		{
			name:    "on_condition_mixing_join_and_literal",
			engines: bothEngines,
			query: "select 1 from purchases p join items i " +
				"on p.id = i.purchase_id and i.qty = 5;",

			// The literal half contributes a query parameter rather than a join.
			wantTables: []string{"items", "purchases"},
			wantJoins:  []string{"purchases(id)=items(purchase_id)"},
			wantIdentifiers: []string{
				"i", "id", "items", "p", "purchase_id", "purchases", "qty",
			},
			wantParams: map[string][]string{"items.qty": {"5"}},
		},
		{
			// The two equalities are one composite key. Generated column by
			// column, a child row could take each column from a different
			// parent row and end up with a pair the parent never had.
			name:    "composite_join_condition",
			engines: bothEngines,
			query: "select 1 from purchases p join items i " +
				"on p.id = i.purchase_id and p.created_at = i.created_at;",

			wantTables: []string{"items", "purchases"},
			wantJoins:  []string{"purchases(id,created_at)=items(purchase_id,created_at)"},
			wantIdentifiers: []string{
				"created_at", "i", "id", "items", "p", "purchase_id", "purchases",
			},
			wantParams: map[string][]string{},
		},
	}

	for _, tc := range cases {
		for _, engine := range tc.engines {
			t.Run(tc.name+"/"+engine, func(t *testing.T) {
				if tc.gap != "" {
					t.Logf("known gap: %s", tc.gap)
				}
				tables, identifiers, joins, params, err := ParseQuery(tc.query, engine, tc.skipJoins)
				if err != nil {
					t.Fatalf("ParseQuery returned an error: %v", err)
				}

				assertSet(t, "tables", tables, tc.wantTables)
				assertSet(t, "identifiers", identifiers, tc.wantIdentifiers)
				assertJoins(t, joins, tc.wantJoins)
				assertParams(t, params, tc.wantParams)
			})
		}
	}
}

func TestParseQueryUnknownEngine(t *testing.T) {
	if _, _, _, _, err := ParseQuery("select 1 from t1;", "oracle", false); err == nil {
		t.Fatal("expected an error for an unimplemented engine, got nil")
	}
}

func TestParseQueryInvalidSQL(t *testing.T) {
	for _, engine := range bothEngines {
		t.Run(engine, func(t *testing.T) {
			if _, _, _, _, err := ParseQuery("select from where;", engine, false); err == nil {
				t.Fatal("expected a parse error, got nil")
			}
		})
	}
}

// formatJoin renders a VirtualJoin as "left_table(col,col)=right_table(col,col)".
func formatJoin(j VirtualJoin) string {
	return j.Left.Table + "(" + strings.Join(j.Left.Columns, ",") + ")=" +
		j.Right.Table + "(" + strings.Join(j.Right.Columns, ",") + ")"
}

// assertJoins compares joins as a multiset. Emission order follows AST
// traversal, which the refactor is free to change; which joins are found is the
// part that must not change.
func assertJoins(t *testing.T, got []VirtualJoin, want []string) {
	t.Helper()

	gotStrs := make([]string, 0, len(got))
	for _, j := range got {
		gotStrs = append(gotStrs, formatJoin(j))
	}
	sort.Strings(gotStrs)

	wantStrs := append([]string(nil), want...)
	sort.Strings(wantStrs)

	if !equalStrings(gotStrs, wantStrs) {
		t.Errorf("joins mismatch\n got: %v\nwant: %v", gotStrs, wantStrs)
	}
}

func assertSet(t *testing.T, label string, got map[string]struct{}, want []string) {
	t.Helper()

	gotKeys := make([]string, 0, len(got))
	for k := range got {
		gotKeys = append(gotKeys, k)
	}
	sort.Strings(gotKeys)

	wantKeys := append([]string(nil), want...)
	sort.Strings(wantKeys)

	if !equalStrings(gotKeys, wantKeys) {
		t.Errorf("%s mismatch\n got: %v\nwant: %v", label, gotKeys, wantKeys)
	}
}

func assertParams(t *testing.T, got map[string][]string, want map[string][]string) {
	t.Helper()

	if len(got) != len(want) {
		t.Errorf("queryParams mismatch\n got: %v\nwant: %v", got, want)
		return
	}
	for key, wantValues := range want {
		gotValues, ok := got[key]
		if !ok {
			t.Errorf("queryParams is missing key %q\n got: %v\nwant: %v", key, got, want)
			continue
		}
		if !equalStrings(gotValues, wantValues) {
			t.Errorf("queryParams[%q] mismatch\n got: %v\nwant: %v", key, gotValues, wantValues)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestParseQueryIsStateless guards the property the scope refactor introduced:
// two calls share nothing, so a name bound by one query cannot leak into the
// next. Before scopes, the alias map was package-level and a binding survived
// into any later query that did not rebind that name.
func TestParseQueryIsStateless(t *testing.T) {
	// Binds "x" to a real table.
	primed := "select 1 from products x where x.company = 'acme';"
	// References "x" without declaring it, so "x" can only be read as a table
	// name of its own. Had the previous query's binding survived, the
	// parameter would be filed under products instead.
	bare := "select 1 from order_items oi where x.company = 'acme';"

	_, _, _, first, err := ParseQuery(bare, "pg", false)
	if err != nil {
		t.Fatalf("ParseQuery(bare) returned an error: %v", err)
	}
	if _, ok := first["x.company"]; !ok {
		t.Fatalf("expected the parameter to be filed under x.company, got %v", first)
	}

	if _, _, _, primedParams, err := ParseQuery(primed, "pg", false); err != nil {
		t.Fatalf("ParseQuery(primed) returned an error: %v", err)
	} else if _, ok := primedParams["products.company"]; !ok {
		t.Fatalf("expected the primed query to bind x to products, got %v", primedParams)
	}

	_, _, _, second, err := ParseQuery(bare, "pg", false)
	if err != nil {
		t.Fatalf("ParseQuery(bare, second call) returned an error: %v", err)
	}

	assertParams(t, second, first)
}
