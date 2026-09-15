# Improvement E — control physical correlation

A plan for the last open item of `TODO.md`, written to be reviewed before any of it
is built.

> Nothing exposes whether a foreign key column is correlated with insert order, yet it
> decides an index scan's cost outright: in the CTE case the dominant node costs 54,533
> when `order_items` is written in `order_id` order and 119,836 when it is not.

## 1. Why the figure moves so much

Postgres stores `pg_stats.correlation` per column: how closely the column's ordering
follows the table's physical ordering, between -1 and 1. `cost_index` does not use it
as a tie-breaker, it interpolates the whole I/O cost with it:

```
    max_IO_cost  = one page read per tuple fetched      (random order)
    min_IO_cost  = one page read per page touched       (perfect order)
    run_cost     = max_IO_cost + correlation^2 * (min_IO_cost - max_IO_cost)
```

So the same index scan over the same rows can cost anything between those two bounds,
and the 2.2x seen in the study is an ordinary spread rather than an extreme one. It is
also invisible to every check this tool currently offers: row counts, selectivities,
distinct counts and page counts can all match while the plan is wrong, which is exactly
what happened to the run that reached the right shape by clustering the table.

## 2. What the tool does today

Nothing deliberate. The physical order a run produces is a side effect of three things:

- **the sampler**. `UniformSample` walks the parent by `LIMIT/OFFSET` under a shared
  cursor, so the parent keys it hands out are ascending. `DBRandomSample`,
  `BoxMullerSample` and `ZipfSample` draw rows with no order at all.
- **the worker pool**. `Insert.run` hands bulks to `--workers` goroutines. Each
  generates its own bulk and then takes `insertMutex` to write it, so bulks reach the
  table in whatever order the workers finish, not in the order their parent pages were
  reserved. With `--workers=3` the ascending cursor is already shuffled three ways.
- **the bulk size**. Within one bulk the rows keep generation order, so a run is
  locally ordered in stretches of `--bulk-size` rows even when it is globally random.

The upshot: `--sequential` produces a partially correlated table, everything else
produces an uncorrelated one, and neither is stated, requested, or checked. That is the
"accidental" in the TODO entry.

## 3. Proposed interface

One option per relationship, keyed by parent table, read with the same
`ParseRelationshipFloat` the sampler tuning already uses:

```
--correlation=1                      every relationship, ascending
--correlation="order_items=1"        one relationship
--correlation="0;order_items=1"      a default, and one exception
```

with two spellings for the ends of the range, because they are what a caller actually
reaches for:

```
--correlated=order_items             the same as --correlation="order_items=1"
--scattered=order_items              the same as --correlation="order_items=0"
```

Range and meaning:

| value | meaning |
|-------|---------|
| `1` | the child is written in ascending parent-key order |
| `0` | the child is written in an order unrelated to the parent key |
| `-1` | descending: rare, but it is a real shape and it costs the same as `1` |
| `0.7` | in between, and the honest target when a real table is neither |

`--correlation` describes the relationship being sampled, not the table being filled,
which is why it is keyed by parent table like `--coin-flip-percent` and `--normal-mean`.

## 4. Implementation

### Stage 1 — make the insert order deterministic

A correlated table cannot be produced while bulks reach it in the order workers happen
to finish. `Insert.run` already numbers its jobs; the change is to give each bulk a
ticket and have `insert` wait for its turn:

```go
type orderedInserter struct {
    mutex sync.Mutex
    cond  *sync.Cond
    turn  int64
}
```

Generation stays parallel — it is the slow half — and only the `INSERT` is serialised,
which it already is by `insertMutex`. The cost is that a worker finishing bulk 7 while
bulk 5 is still being generated has to wait, so a run with an unlucky bulk is as slow as
that bulk. It is opt-in: without `--correlation` the current free-for-all is kept.

### Stage 2 — make the sampler order-preserving

`--correlation=1` requires the parent keys handed to bulk *n* to be greater than those
handed to bulk *n-1*, which only `UniformSample` does. Two ways out, and the choice
wants a decision:

- **(a) force the sampler.** A relationship asked to be correlated is sampled
  sequentially whatever `--default-relationship` says, with one `Info` line saying so.
  Simple, and it silently drops the requested distribution: a correlated *and* zipf-skewed
  relationship is not expressible.
- **(b) sort what was drawn.** Any sampler draws its rows as it likes; the bulk is then
  sorted by parent key before being written. Correlation and distribution stay
  independent, at the cost of sorting each bulk and of correlation that is only as good
  as the bulk boundaries — rows are ordered within a bulk and the bulks are ordered
  between themselves, so the result is properly ascending overall.

(b) is the better answer and is barely more code: the sampled values are already in a
`[][]Getter` per bulk, and sorting it by the first key column is local. It also keeps
`--correlation` orthogonal to `--binomial`/`--pareto`, which is what makes it composable.

**Recommendation: (b).**

### Stage 3 — partial correlation

`--correlation=0.7` is the interesting case, because a real table is rarely at either
end. Sorting gives 1; shuffling gives 0; the space between is reachable by displacing
each row's sort key by gaussian noise before sorting:

```
    sortKey_i = parentRowNumber_i + N(0, sigma)
```

with `sigma` scaled so the resulting rank correlation lands on the target. The mapping
from `sigma` to Spearman correlation has no closed form worth trusting here, so the
plan is to calibrate it in-process: sort a few thousand synthetic keys at a handful of
`sigma` values, measure the correlation, and interpolate. That is cheap, it happens once
per run, and it makes the flag mean what it says instead of meaning "some noise".

This is the same mechanism one of the hand-written runs reached for, which is a decent
sign that it is the natural one.

### Stage 4 — refuse what cannot be done

A table has one physical order. Two relationships on the same child both asked to be
correlated cannot both be satisfied, and the tool should say so up front rather than
satisfy the first one and stay quiet. The check belongs next to the other up-front
refusals in `cmd/run.go`.

Equally worth refusing: `--correlation` together with `--workers` is fine, but
`--correlation` on a table whose rows are inserted twice to break a self-referencing
loop is not, since the second pass appends after the first.

### Stage 5 — check it

`verify` already reads a table back and holds it against the reported figures.
`pg_stats.correlation` is one more column of `pg_stats`, so:

- `verify` gains a **Correlation** section, comparing the correlation of each foreign
  key column against `--correlation`, or against the reported side's own
  `pg_stats.correlation` when the statistics export carries it
- `export-stat` adds `correlation` to the columns it dumps, so the reported value can
  be carried over at all
- `explain-stat` could in principle invert an index scan's cost back into a correlation,
  the same way it inverts a sequential scan's cost into a page count. Worth doing, and
  worth doing after the rest: it needs the index's own cost terms, not just the node's
  total.

Without stage 5 this feature is another figure nobody can confirm, which is the
situation it exists to fix. It should ship in the same change.

## 5. Limits worth stating in the help text

- **postgres only, in effect.** InnoDB tables are organised by primary key, so a child's
  physical order is decided by its own PK rather than by insert order, and a secondary
  index scan's cost model does not read a correlation at all. On mysql the flag would be
  accepted and would do nothing useful, so it should be refused with a message rather
  than accepted quietly.
- **one order per table**, as above.
- **correlation decays.** Nothing here survives an `UPDATE` or a `VACUUM FULL` on the
  generated table, and `CLUSTER` overwrites it entirely — which is the trap the study
  fell into, since clustering also compacts the table and moves `relpages` under the
  plan's feet.
- **two things now fill keys outside the samplers**, and the sort has to come after
  both. A unique key spread over several foreign keys is filled by walking the cross
  product of its parents, which already imposes an order of its own -- the fastest-moving
  parent comes out strongly correlated and the others come out as a staircase -- so
  `--correlation` on such a key is a second order for the same columns and should be
  refused rather than fought over. An imported key skew overwrites some rows after the
  sampler has filled them, so sorting has to happen after that, not inside the sampler.
- **fillfactor is not addressed.** A correlated table that postgres later has to update
  will drift; reproducing a plan on a table that is then modified is out of scope.

## 6. Testing

- a unit test over the sort-key displacement: for `--correlation` of 0, 0.5 and 1, the
  rank correlation of the produced order lands within a tolerance of the target
- an integration test per engine shape: fill a child with `--correlated`, `ANALYZE`, and
  assert `pg_stats.correlation > 0.95`; the same with `--scattered` asserting
  `abs(correlation) < 0.2`. Both are single statements against `pg_stats`, in the
  existing `main_test.go` harness
- an integration test asserting the refusal when two relationships on one child are both
  asked to be correlated
- a `verify --strict` pass over the correlated fixture, so the new section is exercised

## 7. Open questions

1. **Stage 2, (a) or (b)?** The plan recommends (b), sorting each bulk, which keeps
   `--correlation` independent of the sampler. It costs a sort per bulk and it makes
   `--sequential`'s cursor redundant for correlated relationships.
2. **Should `--correlation` default to anything?** Today's accidental behaviour is
   roughly 0 for random samplers and something like 0.3-0.6 for `--sequential` with
   several workers. Defaulting to "unset, do what we do now" keeps every existing run
   byte-identical; defaulting to 0 makes runs reproducible but changes what
   `--sequential` currently produces.
3. **Is negative correlation worth carrying?** It costs nothing to support in the sort,
   and it is a real shape, but it has never come up in a reproduction.
4. **How far should stage 5 go for this change?** The `verify` section and the
   `export-stat` column are small. Inverting an index scan's cost back into a
   correlation in `explain-stat` is a larger piece and could follow separately.
