package cmd

import (
	"fmt"

	"github.com/alecthomas/kong"
)

// tuningHelpFlag prints the long explanation of what each tuning flag moves,
// and exits.
//
// It hooks BeforeReset, the same point kong's own --help uses, so it runs
// before required flags are checked: asking what --rows is for should not
// require passing --rows first.
type tuningHelpFlag bool

func (t tuningHelpFlag) IgnoreDefault() {}

func (t tuningHelpFlag) BeforeReset(ctx *kong.Context) error {
	fmt.Fprint(ctx.Stdout, tuningHelp)
	ctx.Kong.Exit(0)
	return nil
}

// tuningHelp is the reasoning behind the flags, kept out of the flags
// themselves. Each entry of --help is then one line saying what the flag
// moves, and this says why you would move it.
const tuningHelp = `Tuning a reproduction
=====================

A plan is chosen from statistics, not from rows. The planner never looks at
the data: it looks at how many rows a table holds, how wide they are, how many
pages that comes to, how often a value occurs, and how many rows a key brings
back. Filling a schema with plausible-looking rows therefore reproduces
nothing on its own.

Every flag below exists to move one of those numbers. The question to keep
asking is: which statistic am I trying to move, and which flag moves it?

  what it decides          the flags that move it
  ----------------------   --------------------------------------------------
  table size               --rows
  row width, page count    --no-skip-fields, --target-bytes-per-row,
                           --target-relpages, --stat-file
  how often a value        --stat-file, --values-freq-map, --null-freq,
                           --query-param-freq
  how a key fans out       --default-relationship, --binomial, --sequential,
                           --normal, --pareto, --coin-flip-percent,
                           --normal-stddev, --normal-mean, --pareto-s,
                           --pareto-v
  how deep a tree goes     --self-fk-roots, --self-fk-depth, --stat-file
  which tables and keys    --query, --table, --add-fk, --no-fk-guess,
                           --fill-fk-parents
  running it again         --truncate

"explain-stat" reads a reported EXPLAIN and prints most of these numbers as
flags. "verify" reads the filled database back and prints them next to what
was asked for. Both take the same plan file.


Table size
----------

--rows takes one number for every table, a number per table, or both at once:

    --rows=1000
    --rows="orders=500000;order_items=1500000"
    --rows="1000;orders=500000"

The per-table entry wins for that table; the bare number covers everything
else. Filling every table equally is what a real schema never looks like, and
a 50M-row fact table against a 200-row dimension table is what produces a hash
join in the first place.


Row width and page count
------------------------

A sequential scan is costed from the page count, and the page count follows
from how wide a row is. Both are as load-bearing as the row count, and neither
is visible in the row counts alone.

--no-skip-fields turns off the column whitelist. With a --query, only the
columns that query mentions are generated, so rows come out narrower than
production, the table takes fewer pages, a sequential scan looks cheaper than
it is, and the plan flips. It is off by default and it is usually wrong when
reproducing a plan.

--target-bytes-per-row aims a table at an average row width, in bytes, by
writing longer or shorter values into its free-text columns. It is the figure
a plan's "width=" is built from.

--target-relpages aims at a page count instead, which --rows and postgres'
page layout turn back into a row width. Page count is what a sequential scan's
cost is built from, so it is usually the figure a reproduction has to hit.
Postgres only.

Both take the per-table form: --target-relpages="orders=8045;customers=3975".
Neither can work on a table whose generated columns hold no free text to
stretch, which the run says when it happens.

--stat-file sets the width on its own, for nothing. pg_stats holds an
avg_width per column, and added up over a table's columns that is the width of
a row, measured on the database being reproduced rather than worked out by
hand. Either flag above overrides it.

It only does so for a table the dump covers whole. The columns of a dump
narrowed by a --query add up to less than a row, and a width target that is
too low is worse than none: the table takes fewer pages than it should, a
sequential scan looks cheaper than it is, and the plan flips. The run names the
columns it is missing and leaves the width alone. "export-stat --table=NAME",
with no --query, dumps the table whole.


How often a value occurs
------------------------

A filter's selectivity decides join order, so the fraction of rows matching
"status='cancelled'" matters more than which rows they are.

--stat-file replays a column statistics export: null_frac, most_common_vals,
most_common_freqs and avg_width, for every column it covers. It is the
sharpest input there is, because it is a measurement rather than a guess.
"export-stat" prints the command that produces the file. What it does with
avg_width is under "Row width and page count" above.

--values-freq-map injects chosen values at chosen frequencies, per column:

    --values-freq-map="t1.c1=val1:0.75,val2:0.23;t1.c2=10:0.99"

--null-freq is how often a nullable column is NULL, as a fraction. One number
for every column, per column, or both:

    --null-freq="0.1;items.tags=0.73;items.price=0"

A null fraction belongs to a column, not to a table, so an entry naming only a
table is refused.

--query-param-freq inserts the literals the --query compares a column to, on
this fraction of the rows, so the query returns something at all. = and IN are
handled. It defaults to 0, because a selectivity nobody measured is a bad
default: putting 'cancelled' on 10% of rows when the reported side had 3.98%
turns a bitmap scan into a sequential scan. Set it when you have no export and
need rows back; it then overrides what --stat-file says about those values.
Either way the run names the predicates nothing will match.


How a key fans out
------------------

How many child rows a parent gets is what an index scan's loop count and a
join's row estimate are made of. A relationship is sampled from the parent
table, so what to set depends on that parent, and every flag here takes the
per-parent form.

--default-relationship picks the sampler for every relationship, and
--binomial / --sequential / --normal / --pareto override it for named ones,
written "parent_table=child_table":

    --sequential="citizens=ssns"
    --binomial="customers=orders;orders=items"

  binomial    repeated coin flips, postgres TABLESAMPLE BERNOULLI or mysql
              RAND() < 0.1. The default, and right for most 1:N keys
  sequential  SELECT ... LIMIT x OFFSET y. One child per parent, or a
              deliberately flat fan-out where every parent is used
  normal      box-muller, a bell around a point in the parent
  pareto      zipf, for a hot head and a long tail

normal and pareto need a full table scan per sample, so they are slow.

--coin-flip-percent sets how likely each parent row is to be picked, for
binomial. 10 means a 10% chance per row. It interacts with --bulk-size: the
flips are a full scan limited to --bulk-size rows, so a high percentage keeps
finding the first rows and makes them hot, while a very low one slows sampling
down. What the right value is depends on the parent's row count, hence:

    --coin-flip-percent="1;orders=3;products=5"

--normal-stddev defaults to a tenth of the parent's row count, --normal-mean
to the middle of it. --pareto-s is the slope, above 1, higher decaying faster
so the first rows run hotter; --pareto-v maps to V in math/rand.Zipf and must
be at least 1.


How deep a tree goes
--------------------

A table with a foreign key on itself -- employees and their manager, categories
and their parent -- is inserted as a tree, level by level: the roots first,
their parent key NULL, then each level pointing at the one before. A recursive
CTE walks one level per iteration, so the depth is its loop count, and the
share of roots is the null_frac of the parent key.

--self-fk-roots is the share of the rows that are roots, --self-fk-depth the
number of levels, roots included. Each level is the one before it times the
same factor, picked so that the roots get their share and the levels add up
to --rows:

    --rows=1000 --self-fk-roots=0.1  --self-fk-depth=4   100 166 276 458
    --rows=1000 --self-fk-roots=0.25 --self-fk-depth=4   250 250 250 250
    --rows=1000 --self-fk-roots=0.7  --self-fk-depth=3   700 227  73

A share below 1/depth fans out, an org chart; above it the levels peter out,
a comment thread. The run logs the sizes it picked. Both take the per-table
form, --self-fk-depth="employees=6;categories=3", and default to half the rows
as roots over 2 levels. --stat-file sets the share of roots from the null_frac
of the parent key when --self-fk-roots is not given.

A level samples its parents by coin flip, as binomial does, out of the level
before it only, so every row sits exactly as deep as the level it went in at
and a recursive CTE walks exactly --self-fk-depth levels. --coin-flip-percent
applies, as it does to any parent. The level is told apart by its key, which
needs one the database numbers as rows come in -- auto-increment, serial,
identity. Without one a level can point at any row already in the table, its
own included, and the run warns. Naming the key in --binomial, --sequential,
--normal or --pareto, as "employees=employees", samples it that way instead.


Which tables and keys
---------------------

--query is the usual way in: it works out which tables to fill, which columns
are needed, and which foreign keys the JOINs imply, including through CTEs and
derived tables. --table narrows a run to one table, or restricts a --query.

A child column with no foreign key gets independent random values, so the join
matches nothing and the query returns no rows. Whatever the schema does not
declare and the query does not imply, declare by hand:

    --add-fk="customers.id=purchases.customer_id;purchases.id=items.purchase_id"

A composite key is written with several columns on each side, so that a child
row takes all of them from one parent row:

    --add-fk="customers.id,created_at=purchases.customer_id,created_at"

--no-fk-guess sticks to the keys the database declares and ignores what the
query implies. --fill-fk-parents adds the tables a key points at to the run
when they hold no row; without it such a run is refused up front, naming them,
rather than failing half way through with some tables already written.

The "query" subcommand prints all of this without touching a database, which
is the cheapest way to find out that the join you care about was not inferred.


Running it again
----------------

--truncate empties the tables this run fills before inserting. Without it a
second run adds to what the first left behind, which is rarely what tuning
wants. It never touches a table the run does not fill: a foreign key pointing
in from outside makes it refuse rather than cascade.

The tool does not collect statistics. Nothing above reaches the planner until
you do:

    VACUUM ANALYZE;                              -- postgres
    ANALYZE TABLE orders, order_items, products; -- mysql

VACUUM rather than a bare ANALYZE on postgres: an index-only scan needs the
visibility map, and a freshly loaded table has none however good its
statistics are.
`
