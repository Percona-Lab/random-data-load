# TODO

Findings from a head-to-head study run on 2026-09-07/08: reproducing a customer
EXPLAIN with this tool versus hand-writing a generator, over three cases —
3 tables / 525k rows, 10 tables / 3.6M rows with a `pg_stats` import, and the
same 10-table schema behind a CTE and a derived-table join. Sixteen runs, all
of which did reproduce their target plan node for node.

Everything below was observed in those runs and re-confirmed by hand against
the binary at 48d2078. What has since been fixed has been taken out; what is
left is still open.

E now has a plan of its own in PLAN-E-physical-correlation.md, written to be
reviewed before any of it is built.

## Bugs

### 1. Literals inside a CTE body are never collected

`traverseQueryParameters` (`query/query.go:93`) walks the parse tree with a
traverser that only handles `ast.Infix`, and `ast.Select.Traverse` does not
descend into `With`. So an `=` or `IN` literal written inside a CTE is never
seen. `traverseIdentifiers` already has the fix for its own traversal — an
explicit `case ast.Select` that calls `n.With.Traverse(traverser)`
(`query/query.go:55-63`) — and the same branch is missing here.

```
CTE body         : queryParams map[customers.segment:[enterprise] products.is_discontinued:[FALSE]]
same, flat WHERE : queryParams map[customers.segment:[enterprise] orders.status:[cancelled] products.is_discontinued:[FALSE]]
```

A predicate inside a *derived table* is collected correctly, so this is
specific to `WITH`. The failure is silent and total: the column gets random
values and the query returns zero rows. It only went unnoticed in the study
because `--stat-file` happened to carry the same column.

Note the traverser at `query/query.go:97` is `traverser := func(...)`, so it cannot call
itself; it needs the `var traverser ast.Traverser` form the other one uses.

### 2. `count(*)` cancels the column whitelist, widening the stats dump

The `*` in `count(*)` hits the `emptyMap = true` branch of
`traverseIdentifiers`, which discards the whole column whitelist. `export-stat`
then emits no `attname` filter and dumps **every column of every table in
scope**:

```
SELECT status FROM orders WHERE status='x'          -> lower(attname) IN ('orders', 'status')
SELECT status, count(*) FROM orders ... GROUP BY 1  -> no column filter at all
SELECT status, count(1) FROM orders ... GROUP BY 1  -> lower(attname) IN ('count', 'orders', 'status')
```

`most_common_vals` holds real values from the customer's database, and nearly
every reporting query contains a `count(*)`, so in practice the dump is almost
always the wide one. A bare `*` in the select list genuinely cannot narrow
columns, but `count(*)` says nothing about which columns are read and should
not cancel the list.

While there: the whitelist collects raw identifiers, so it also contains table
names (`'orders'`) and function names (`'count'`). Harmless as a filter, but it
means the list is not really a column list.

### 10. `--version` prints nothing outside a release build

`main.go:31` builds the version string from ldflags-only variables, so a
`go install` or a local `go build` reports empty values. `debug.ReadBuildInfo()`
gives the module version, VCS revision and dirty flag for exactly these cases
and would let the documented prerequisite check ("v0.2.6 or newer") work.

## Improvements that would cut token usage

The study measured what an agent spends to drive this tool. The headline: the
tool arm cost 1.34x and 1.54x the hand-written arm on the first two cases and
0.94x on the third. What follows is ordered by how much of that gap each item
would actually close, judged by what the sixteen runs demonstrably spent their
turns on.

### E. Control physical correlation

Nothing exposes whether a foreign key column is correlated with insert order,
yet it decides an index scan's cost outright: in the CTE case the dominant node
costs 54,533 when `order_items` is written in `order_id` order and 119,836 when
it is not. One tool run reached the right plan shape with the wrong mechanism —
it clustered the table and landed 33% low on total cost while every row count
still matched, which no row-count check would catch. The hand-written runs paid
heavily here too, one of them resorting to gaussian displacement and patching
`pg_statistic` by hand to confirm the mechanism.

A per-relationship `--correlated` / `--scattered` choice would make this
reachable instead of accidental.

## What already works and should not regress

- **Join inference through CTEs and derived tables.** The parser resolved
  `li.order_id = rc.order_id` — a derived table joined to a CTE, neither of
  them a real table — back to `order_items` and `orders`, and correctly skipped
  all three inferred relationships as already covered by declared foreign keys.
  Every run confirmed it needed no `--add-fk`. Bug 1 above is in literal
  extraction, not in this.
- **Exact row counts.** `--rows-per-table` hits the reported figure exactly on
  every table, where the hand-written arm's `random()` sampling drifts by a few
  thousand rows.
- **Walking the FK graph.** Insert ordering, the composite foreign key and the
  self-referencing table were handled without the caller thinking about them.
