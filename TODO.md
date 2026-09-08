# TODO

Findings from a head-to-head study run on 2026-09-07/08: reproducing a customer
EXPLAIN with this tool versus hand-writing a generator, over three cases —
3 tables / 525k rows, 10 tables / 3.6M rows with a `pg_stats` import, and the
same 10-table schema behind a CTE and a derived-table join. Sixteen runs, all
of which did reproduce their target plan node for node.

Everything below was observed in those runs and re-confirmed by hand against
the binary at 48d2078. The entries marked **Done** have since been fixed on
this branch; the rest are still open.

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

### 3. `char(n)` is refused outright

**Done** in 662786b.

`postgresTypeMapping` (`db/pg.go:13`) maps `character varying` to `varchar` but
has no entry for `character`, which is what `information_schema` reports for
`char(n)`. `isSupportedType` then rejects the column, and a `NOT NULL char(n)`
with no default aborts the run before any insert. `time without time zone` is
missing from the same map.

A one-line entry each. `char(2)` country and currency columns are common enough
that the study's schemas had to avoid them to measure anything else.

### 4. `varchar(2)` columns named `country` produce invalid UTF-8

**Done** in 7c5de6a.

A short `varchar` whose name matches the country generator gets a full country
name, which is then truncated to the column width. Truncating `Åland Islands`
to 2 bytes splits a multi-byte sequence and postgres rejects the whole batch:

```
invalid byte sequence for encoding "UTF8": 0xc3 0x27
```

Hit on `addresses.country` and `warehouses.country` in three separate runs.
Truncation should be rune-aware, and ideally a generator whose values do not
fit the column should not be chosen at all.

### 5. `decimal`/`numeric` ignores scale

**Done** in a66a12a.

`NewRandomDecimal` generates in `[0, precision)` without accounting for the
scale, so `numeric(5,2)` — max 999.99 — receives values up to 99999 and the
insert fails with a range error. Only `numeric(p,2)` with a large `p` is safe.
The study's schemas had to avoid `numeric(3,2)` and `numeric(5,2)` for this
reason. Related: a `decimal` primary key still collides, since the generated
range ignores scale entirely.

### 6. Composite primary keys collide when the sequential walk wraps

Filling `inventory(warehouse_id, product_id)` — a two-column PK sampled from
two parents — fails with `duplicate key value` once the sequential parent walk
wraps around. One run got 300k of the 400k rows it asked for and finished the
remainder with hand-written SQL. Uniqueness has to hold across the whole key,
not per column.

### 7. A query's own table set is never sufficient

`--query` fills the tables the query names, but those tables have `NOT NULL`
foreign keys to tables it does not name, and the run dies partway through:

```
failed to insert on public.products: cannot sample the foreign keys of public.products:
table public.categories is empty, so there is nothing to point a foreign key at.
```

The message is good, and the behaviour is correct rather than broken — but the
tool already knows the full FK closure at that point, so it could either pull
those parents in automatically or refuse up front with the list of tables that
must be filled first, instead of failing after some tables are already loaded.

### 8. Foreign-key column statistics cannot be imported

`--stat-file` matches by table and column name, so it happily accepts the
`most_common_vals` of a foreign key column — but those are literal parent ids
from the customer's database, and this tool assigns random values to `serial`
and `bigserial` keys, so the ids do not exist locally. Every run had to strip
those entries from the dump by hand before importing, or watch the FK sampling
fail.

This is not cosmetic. Postgres reads `most_common_freqs[1]` of the inner join
column to size hash buckets, so join-key skew is what decides which side of a
hash join is the build side. In the 10-table case that was the single hardest
thing to reproduce: one run patched it with SQL after loading, one manufactured
it by abusing `--coin-flip-percent=100 --bulk-size=100`, and one could not flip
it at all.

Worth considering: honour a parent's imported key frequencies by *sampling the
parent rows in that proportion*, which is expressible without inventing ids.

### 9. `--query-param-freq` silently overrides `--stat-file`

Query literals outrank the stat file, so with the default `0.1` a predicate
that appears both in the query and in the dump lands at 10% instead of its real
frequency — `status='cancelled'` at 10% rather than 3.98% is a sequential scan
where the customer had a bitmap scan. Every run that used `--stat-file` had to
discover `--query-param-freq=0` for itself, some after a full reload.

When a column's frequency comes from an imported dump, the measured value
should win over a literal guessed from the query, or at least warn loudly.

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

### A. Read an EXPLAIN and print the statistics it implies

**Done** in 10f0879.

**The single largest cost, and it is paid by both arms.** Every run, with the
tool and without it, hand-decoded the same things from the plan text: page
counts out of sequential-scan costs, filter selectivities out of actual row
counts, `n_distinct` out of a HashAggregate's estimate, fan-out out of an inner
index scan's rows-per-loop, and a date window out of a filter's survival rate.
That is arithmetic, it is identical every time, and it took several turns of
reasoning in each of sixteen runs.

A subcommand taking the target EXPLAIN and printing the implied per-table row
counts, page counts, selectivities and distinct counts — ideally as ready-made
`--rows-per-table` and `--values-freq-map` arguments — would remove the most
expensive shared step in the whole workflow. It would help the hand-written
approach too, but it is the tool that could ship it.

### B. Target a row width or a page count directly

Row width decides `relpages`, `relpages` decides scan costs, and scan costs
decide the plan — including whether postgres parallelises at all. Every run in
both arms solved for a target tuple width and then iterated: load, measure
`relpages`, adjust text lengths, reload. Several built a small-scale calibration
schema first purely to shorten that loop.

A `--target-bytes-per-row` or `--target-relpages` per table, with the filler
distributed across the free-text columns, would replace an entire
load-measure-adjust cycle with one flag.

### C. Make `--coin-flip-percent` per relationship

**Done** in 3a2224d.

It is global, but the right value is a function of one parent's row count and
the bulk size, so a run touching several relationships cannot satisfy them at
once. Every multi-table run worked around this by splitting into one `--table=`
invocation per table, each with its own value — which also means re-deriving
the ratio for each one and writing a much longer repro script.

`--coin-flip-percent=orders=3,products=5` in the style of the existing
`--rows-per-table` would collapse those runs back into one.

### D. Verify a load against the target

Every run ended by hand-writing the same verification SQL: count rows per
table, compute the predicate selectivities, count distinct values, read back
`relpages` and `reltuples`, then compare each against the reported figures.
That is another identical, mechanical step repeated sixteen times.

A `verify` subcommand taking the same `--stat-file` and target EXPLAIN and
printing a reported-versus-generated table would end every run in one call —
and it is exactly the comparison that belongs in a support ticket anyway.

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

### F. Make a run repeatable without a manual reset

**Done** in b852850.

There is no `TRUNCATE`, so a second run appends to the first and every
iteration needs a manual cleanup step. Since tuning is inherently iterative —
load, look at the plan, change one flag, reload — this tax is paid on every
loop. A `--truncate` flag, or refusing to start against a non-empty target
unless one is given, would remove it.

### G. Fix the bugs above, which cost a diagnose-and-retry cycle each

Items 3 through 7 each cost a failed run, a diagnosis and a workaround in the
runs that hit them: the `char(n)` refusal, the UTF-8 truncation crash, the
numeric scale overflow, the composite-key collision and the empty-parent abort.
None of them is conceptually hard, and each one currently converts into several
turns of an agent's time and a paragraph of explanation in whatever script it
writes.

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
