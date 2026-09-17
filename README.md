# Random data generator for MySQL and PostgreSQL
Forked from https://github.com/Percona-Lab/mysql_random_data_load

This tool aims to produce a quick working environment to reproduce a query execution behavior in order to optimize it.
It is meant for cases where we cannot access real data, only schema and cardinalities. 

Based on the table(s) schema and a query, it will generate random data with respect to fields, foreign keys defined in databases, foreign keys infered from the query pattern, (plan: from existing cardinalities and distributions). 

## Usage
`random-data-load run --engine=(mysql|pg) --rows=INT-64 (--query=SELECT ...|--table=table_name) [options...]`


### Example
Using the following schema,
```
CREATE TABLE public.orders (
    order_id integer primary key generated always as identity,
    shipping_address text NOT NULL,
    country text,
    zip text NOT NULL,
    currency character varying(3) NOT NULL,
    email character varying(100) NOT NULL
);

CREATE TABLE public.products (
    id varchar(30) primary key,
    product text NOT NULL,
    price numeric NOT NULL,
    material text,
    feature text,
    company text
);

CREATE TABLE public.order_items (
    product_no varchar(30) NOT NULL,
    order_id integer NOT NULL
);
```

To debug the following query:
```
select sum(p.price), count(oi.*) from orders o join order_items oi on o.order_id=oi.order_id join products p on p.id = oi.product_no where o.currency='EUR';
```

An example of usage:

```
$ time ./random-data-load run --engine=pg --host=127.0.0.1 --user=sbtest --password=sbtest --database=postgres --port=5432 --bulk-size=4500 --rows=500000 --default-relationship=binomial --coin-flip-percent=1 --query-param-freq=0.1 --query="select sum(p.price), count(oi.*) from orders o join order_items oi on o.order_id=oi.order_id join products p on p.id = oi.product_no where o.currency='EUR';" 
Writing orders (337500/500000) rows...
Writing orders (500000/500000) rows...
Writing products (500000/500000) rows...
Writing order_items (499500/500000) rows...

real	0m16,168s
user	0m16,549s
sys	0m1,181s

postgres=# select sum(p.price), count(oi.*) from orders o join order_items oi on o.order_id=oi.order_id join products p on p.id = oi.product_no where o.currency='EUR';
     sum     | count 
-------------+-------
 1595.505421 |  3231
(1 row)

postgres=# select * from products limit 10;
          id          |             product              |  price   | material  |     feature      |            company             
----------------------+----------------------------------+----------+-----------+------------------+--------------------------------
 sfkes5nhpegtt977ae2b | Mighty Desk Lamp Quick           | 0.043675 | carbon    | impact-resistant | PeerJ
 uht6n748y9ghghe7gdqa | Practical Ashtray                | 0.684435 | slate     | plug-and-play    | EMC
 fyyf5kgkdj7d87aa7g2c | Incredible Memory-Enabled Grater | 0.007092 | tungsten  | wrinkle-free     | Outline
 cetyjbc84bgfdrjrdrm2 | Self-Adjusting Alarm             | 0.710173 | limestone | led-backlit      | Wolters Kluwer
 mbk78nvxqqpmc3yeep24 | Steam-Powered Rocking Chair      | 0.235886 | silver    | resistant        | ConnectEDU
 aawjj9ce27q88mm3fysg | Vinyl Bag                        | 0.067065 | iron      | interactive      | The Advisory Board Company
 4fpym2hnm45erv9c5hdw | Artistic Window Blind            | 0.759076 |           | resistant        | IVES Group Inc
 rgxvextkvyz8nhw79btp | Treasure Chest Anti-Slip Quick   | 0.825359 | paper     |                  | Business Monitor International
 t6ng73kmpe7esnjugf66 | Tactical-Revolutionary Cooker    | 0.427905 | composite | rust-proof       | LoopNet
 y35yfc7m2stt6zxh4pqz | Lawn Mower Hemp Express          | 0.181523 |           | energy-efficient | SpaceCurve
(10 rows)

postgres=# select * from orders limit 10;
 order_id |     shipping_address      |  country   |  zip  | currency |            email             
----------+---------------------------+------------+-------+----------+------------------------------
   414763 | 93265 North Rampville     | Belgium    | 17807 | GMD      | mollyhoffman@maxwell.biz
   414764 | 4359 North Summitburgh    | Egypt      | 25582 | VES      | arnoldwilkinson@gislason.org
   414765 | 28909 Ranchmouth          | Mauritania | 32167 | ANG      | kendallgleichner@pena.biz
   414766 | 8214 North Keyton         | Ecuador    | 72284 | AZN      | aaronvillarreal@lambert.info
   414767 | 657 Loafbury              |            | 63499 | BND      | clairedooley@gross.name
   414768 | 826 East Tunnelview       | Réunion    | 20814 | CDF      | ardenhamilton@barnett.org
   414769 | 176 Lake Underpassborough | Gambia     | 81642 | CHF      | adriancummings@knight.org
   414770 | 87086 Rowhaven            | Armenia    | 68902 | MZN      | dexterstanton@payne.com
   414771 | 5421 West Lodgeshire      |            | 54406 | EGP      | ezekielrivera@matthews.io
   414772 | 50778 Lake Unionsside     | Kuwait     | 30627 | GYD      | christaball@cruz.biz
(10 rows)
```

## Options

Common options:

|Option|Description|
|------|-----------|
|--engine|mysql/pg|
|--host|Host name/ip|
|--user|Username|
|--password|Password|
|--port|Port number|
|--quiet|Do not print progress bar|
|--dry-run|Print queries to the standard output instead of inserting them into the db|
|--debug|Show some debug information|
|--pprof|Generate pprof trace at --cpu-prof-path. Also opens port 6060 for pprof go tool|
|--version|Show version and exit|
|--rows|Number of rows to insert. One number for every table, or per table, or both: `--rows="1000;orders=500000;order_items=1500000"` fills orders and order_items with their own counts and everything else with 1000|
|--bulk-size|Number of rows per INSERT statement (Default: 1000)|
|--workers|how many workers to spawn. Only the random generation and sampling are parallelized. Insert queries are executed one at a time (Default: 3)|
|--table|Table to insert to. When using --query, --table will be used to restrict the tables to insert to.|
|--query|Providing a query will analyze its schema usage, insert recursively into tables, and identify implicit joins|
|--no-skip-fields|Disable field whitelist system. When using a --query, it will get the list of fields being used as a whitelist in order to generate the minimal sets of fields required, unless --no-skip-fields is being used or any * has been found.|
|--null-freq|How often a nullable column is NULL, as a fraction between 0 and 1. One number for every column, or per column, or both: `--null-freq="0.1;t1.c1=0.73;t1.c2=0.04"` leaves every other column at 10% and sets 73% and 4% on those two (Default: 0.1)|
|--values-freq-map|Inject arbitrary values at fixed frequencies. The format is "--values-freq-map=t1.c1=val1:0.75,val2:0.23;t1.c2=10:0.99" so that val1 will be on 75% of rows and val2 on 23% for column c1|
|--query-param-freq|Insert the literals the `--query` compares a column to, on this fraction of the rows, so the query returns something. Defaults to 0: it is a selectivity nobody measured, so it is only applied when asked for, and it then overrides anything `--stat-file` says about those values. The run names the predicates nothing will match|
|--stat-file|Scan a column statistics export and reuse its null_frac, most_common_vals and most_common_freqs instead of setting --null-freq and --values-freq-map by hand. Use the `export-stat` subcommand to get the command producing that file|
|--min-generated-time|Generated timestamps will be after this date. Format is RFC3339. Will default to --max-generated-time - 1 year|
|--max-generated-time|Generated timestamps will be before this date. Format is RFC3339. Will default to now()|

Foreign key sampling options:
|Option|Description|
|------|-----------|
|--add-fk|Add foreign keys, if they are not explicitely created in the table schema. It can complement the foreign keys guessed from the --query, or be used to manually define foreign keys when using --no-fk-guess too. Format: --add-fk="parent_table.col1[,col2...]=child_table.colx[,coly...][; additional fk ]". Example: --add-fk="customers.id,created_at=purchases.customer_id,created_at;purchases.id=items.purchase_id"|
|--no-fk-guess|Do not try to guess foreign keys from the --query missing in the schema. When a query is provided, it will analyze the expected JOINs and try to respect dependencies even when foreign keys are not explicitely created in the database objects. This flag will make the tool stick to the constraints defined in the database only, unless you add foreign keys manually with --add-foreign-keys.|
|--default-relationship|Will define the default foreign-key relationship to apply. Possible values: binomial,sequential. The default relation can be overriden with other parameters --binomial or --sequential|
|--binomial|Defines a 1-N foreign key relationships using repeated coin flips. Postgres' tablesamples Bernouilli or mysql RAND() < 0.1 (can be tuned with --coin-flip-percent). Format should be "parent_table=child_table". E.g: --binomial="customers=orders;orders=items"|
|--coin-flip-percent|When used with --binomial, it will set the likeliness of each rows to be sampled or not. 10 would mean each rows have only 10% chance to be selected when sampling a parent table. Using large values will favor hot rows: the coin flips are done with a table full scan, with a limit set at --bulk-size, so with a large percent chance most of the time the first rows will be selected. No effects when used with --sequential. It is raised automatically, per relationship, when the parent being sampled is too small for it to bring anything back (Default: 1)|
|--sequential|Defines a sequential foreign key links relationships. Format should be "parent_table=child_table". E.g: --sequential="citizens=ssns". The relationship is 1-1 for as long as the parent has rows left, and a round robin over the parent past that|
|--normal|Defines a 1-N foreign key relationships using box-muller transformation to provide normal distribution. Slow method needing full table scans for each samples.|
|--normal-stddev|Standard deviation to the normal law. Will default to 1/10 of the row count of the parent table being sampled|
|--normal-mean|Mean of the normal law. Will default to the middle of the parent table being sampled|
|--pareto|Defines a 1-N foreign key relationships using zipf (pareto) distribution. Slow method needing full table scans for each samples|
|--pareto-s|Zipf slope parameter. Must be above 1. Higher value will mean faster decay, so first rows will be hotter|
|--pareto-v|Must be >=1. Directly map to V, https://pkg.go.dev/math/rand#Zipf.|

### Example

Continuing the example with orders, products and order_items:
```
-- how many times products are present in order_items
postgres=# select oi.product_no, count(*) from order_items oi group by 1 order by 2 desc limit 10;
      product_no      | count 
----------------------+-------
 gg476vcr2fa9pdmhazhb |     9
 7vzsn676dzyyyb3b2wv8 |     9
 sny5dzjhjp2zhk6zbxad |     8
 eemd8eng9d8sgk2m2zeg |     8
 4eahk4nur48t8bcmqq35 |     8
 b5cemgse4ybzkbxuqwdf |     8
 7yv82qvg3g5mgpvfggv4 |     8
 h3zhu5kwm2frqkgb3c5p |     8
 3hjg6w6nmrx2z5g66z2d |     8
 akjkd45a7k4h3mcwrsg7 |     8
(10 rows)

-- how many unique products
postgres=# select count(distinct oi.product_no) from order_items oi;        
 count  
--------
 303943
(1 row)


-- how many unique order ids in order_items. 500k is because of --sequential and --rows being equal between tables
postgres=# select count(distinct oi.order_id) from order_items oi;
 count  
--------
 500000
(1 row)


```

Changing the data distribution with a higher --coin-flip-percent:

```
postgres=# truncate products, orders, order_items;
TRUNCATE TABLE

./random-data-load run --engine=pg (...) --coin-flip-percent=30 (...)

-- still a similar result
postgres=# select sum(p.price), count(oi.*) from orders o join order_items oi on o.order_id=oi.order_id join products p on p.id = oi.product_no where o.currency='EUR';
     sum     | count 
-------------+-------
 1559.053189 |  3110
(1 row)

-- But the data repartity of product ids is different, some products are more "hot"
postgres=# select oi.product_no, count(*) from order_items oi group by 1 order by 2 desc limit 10;
      product_no      | count 
----------------------+-------
 2cqz6jvnz7avrt59ahgm |    53
 2vf499qtfkd34th5bat2 |    52
 2rnv6yhj47k3m29svggq |    51
 2kqhvjk99c7pftfjqn4n |    50
 2ev2dmtajgh49k9cdupv |    50
 2kph4hmd2w29n2dsmh8r |    50
 284tqufe3psbbyd6r5kb |    50
 29gp22hggwygagdsvx7g |    50
 2ajgrfbe6ww3neg6xc3f |    49
 2afye7ytsxz6afyhr6ku |    49
(10 rows)

-- There's way less diversity of products, ~485k products don't even have 1 order
postgres=# select count(distinct oi.product_no) from order_items oi;
 count 
-------
 15357
(1 row)

-- order ids sampling is still sequential, so identical
postgres=# select count(distinct oi.order_id) from order_items oi;
 count  
--------
 500000
(1 row)


```

If 15k referenced products isn't diverse enough, we can work with higher --bulk-size.
This is because sampling is limited to --bulk-size with a LIMIT BY --bulk-size, so low --bulk-size with high --coin-flip-percent will ultimately lead to the very first sampled rows repeated too often

```
postgres=# truncate order_items;
TRUNCATE TABLE

-- we'll restrict to just order_items not to re-insert orders or products.
./random-data-load run --engine=pg (...) --coin-flip-percent=30 --bulk-size=30000 --table=order_items (...)


-- more product diversity
postgres=# select count(distinct oi.product_no) from order_items oi;        
 count  
--------
 100395
(1 row)

-- which will mean a lesser "max" usage of a single product. Higher --coin-flip-percent  could force hotter rows again
postgres=# select oi.product_no, count(*) from order_items oi group by 1 order by 2 desc limit 10;
      product_no      | count 
----------------------+-------
 68uzayu85vgbcfy2fand |    14
 2x6pg2wztdeq6gxsj7je |    13
 4dmzwejn2g8kfx5ak774 |    13
 6jy36mxygtyf3yvqph6f |    13
 7yfnr2nsqqeud5d9543w |    13
 4ncnh6nr2km6ddwya7wn |    13
 2h26zpsmsucgg4a2gnrh |    13
 22gkgw4egqemr5ht5fhx |    12
 594p72pt9wjmva3xwnm6 |    12
 48jsvueqqhw9webchje7 |    12
(10 rows)

```

## Foreign keys support
If a field has Foreign Keys constraints, `random-data-load` will get samples from the referenced tables in order to insert valid values for the field.  
To enforce orders, an arbitrary 'ORDER BY 1' is made. This is so that --sequential can create 1-1 relationship, and to better master the eventual distribution of --binomial.

Composites foreign keys are supported.
With very low chances to sample rows, we might sample too little. The tool will loop until it sampled enough rows to fill the next bulk insert.

A parent key is read whatever its type, including the types no value can be generated for: a `uuid`, `numeric` or `boolean` primary key is read and copied verbatim into the child, so a column this tool cannot invent a value for can still be pointed at. Note that postgres reports both `numeric(p,s)` and `decimal(p,s)` as `numeric`.

Every distribution is measured against the parent table it samples, not against --rows: --coin-flip-percent is raised when the parent is too small for it (a 1% coin flip over a 500-row dimension table is expected to return 5 rows, and returns none often enough for it to be the normal outcome), --normal-mean and --normal-stddev default to the middle and a tenth of the parent, and --sequential wraps back to the parent's first row once it has handed out all of them.

An empty parent table is refused, naming it: there is nothing for a foreign key to point
at, and the child rows it cannot fill are not silently left out. That check now happens
before anything is written rather than when the first sample is taken. A `--query` names
the tables it reads, and those tables have NOT NULL foreign keys to tables it does not
name; the run used to discover that part way through, with the tables ahead of it in the
insert order already loaded. The whole closure is walked up front and refused in one
message naming every table that has to be filled first, or, with `--fill-fk-parents`,
those tables are added to the run and filled with `--rows`. A parent
that already holds rows is left alone either way.

A unique key whose columns come from **several** foreign keys is filled from all of them
at once. Filled one key at a time, each sampler walks its own parent and behaves
perfectly on its own column, but the combinations they produce start repeating as soon
as the shortest walk comes round again: `inventory(warehouse_id, product_id)` over 100
warehouses and 4,000 products repeats a pair every 4,000 rows whatever either sampler
does, and the primary key refuses it. Instead the run walks the cross product of those
parents, reading each row's position like an odometer, so a combination only repeats
once every one of them has been used. The sampling options do not apply to such a key —
the walk is what keeps it unique — and asking for more rows than the parents can make
combinations is refused, naming the counts, before the first row of the child is
written.

That walk reads parent rows by position with `ROW_NUMBER()`, so it needs MySQL 8.0. The
Pareto and normal samplers below still use user variables and still work on 5.7.

**1.** sequential relationships will sample with LIMIT and OFFSET:  
```
SELECT <field[, field2]> FROM <referenced schema>.<referenced table> ORDER BY 1 LIMIT <--bulk-size> OFFSET y
```
This isn't the fastest method but it works for every types and compound primary keys. The value of the current OFFSET is protected by mutex to prevents frequent duplicates. 

**2.** binomial relations will sample differently between postgres and mysql

**2.1** For postgres it relies on TABLESAMPLE
```
SELECT <field[, field2]> FROM <referenced schema>.<referenced table> TABLESAMPLE BERNOUILLI (<--coin-flip-percent>) ORDER BY 1 LIMIT <--bulk-size>
```

**2.2** For mysql, it relies on RAND()
```
SELECT <field[, field2]> FROM <referenced schema>.<referenced table> WHERE rand() < (<--coin-flip-percent>/100) ORDER BY 1 LIMIT <--bulk-size>
```

**3.** Pareto and normal distribution
Both methods are implemented using row_number()
Postgres uses row_number()
```
select <fields,..> from (SELECT columns, ROW_NUMBER() OVER (ORDER BY <fields...>) as rownumber FROM table ) f where rownumber IN (x1, x2, ...) and <checking fields not to be null> order by 1 limit <--bulk-size>
```
While MySQL is still implemented with user variables to retain mysql 5.7 compatibility
```
select <fields,...> from table, (SELECT @rownumber := 0) f where (@rownumber := @rownumber + 1) IN (x1, x2, ...) and <checking fields not to be null> order by 1 limit <--bulk-size>
```

**3.1** Pareto
"pareto" is actually using zipf random number generation. The slope can be tuned with --pareto-s such as higher value will mean faster decay. The other parameter --pareto-v is not documented in its related go stddlib package for now.
First rows will be hotter and sampled far more commonly, but it will nonetheless retain a long "tail" over the whole table.

**3.2** Normal
"normal" is actually implemented using box-muller transformation (reproducing "normal" distribution from 2 uniformly random float numbers between 0.0 and 1.0)
It will mostly sample around the --normal-mean based on --normal-stddev, and very few rows on the outlier parts.

## Guessing implicit foreign keys from queries
If no foreign keys are explicitely defined in the schema, but the query requires columns to match, `random-data-load` will infer the foreign keys and insert valid values so that the query returns rows.
Can be disabled with --no-fk-guess

An estimation can be made using:
```
random-data-load query --query="$(cat huge_select.sql)"
``` 

Foreign keys are guessed from:
- JOINs with an ON clause, parenthesised or not
- JOINs written implicitely, with the condition in the WHERE clause: `FROM x, y WHERE x.a = y.b`
- correlated subqueries: `WHERE EXISTS (SELECT 1 FROM y WHERE y.a = x.b)`
- semi-joins: `WHERE x.a IN (SELECT y.b FROM y)`

References are followed through subqueries and CTEs down to the real tables they read, so a query joining on a derived result generates data in the underlying tables:
```
WITH recent AS (SELECT order_id FROM orders WHERE currency = 'EUR')
SELECT count(*) FROM recent r JOIN order_items oi ON r.order_id = oi.order_id;
```
generates `orders` and `order_items`, and the foreign key lands on `orders.order_id`. The same holds for derived tables, for a CTE reading from an earlier CTE, and through renamings, whether by a column alias or a CTE column list. When a CTE or subquery is a UNION, one foreign key is generated per branch, since the value may come from any of them. A recursive CTE contributes its anchor branch; the self-reference is ignored.

A condition spanning several columns is generated as one composite foreign key rather than one key per column, so that a child row takes all its columns from the same parent row:
```
FROM purchases p JOIN items i ON p.id = i.purchase_id AND p.created_at = i.created_at
```

It will not guess a foreign key for:
- conditions other than equality: `ON x.a > y.b` does not require the values to match
- negated conditions, including `NOT IN`, which ask for the values to stay apart
- values that cannot be traced back to a column, such as an aggregate or an expression in a subquery's SELECT list. These are reported with a warning naming the condition, since the query will not return rows without them
- JOINs using a USING clause. Write the condition with ON, or declare it with --add-fk
- JOIN conditions using ambiguous columns, without expliciting to what table it belongs. Example `FROM x JOIN y ON apple=pear` instead of `FROM x JOIN y ON x.apple=y.pear`

Conditions on either side of an OR are kept as separate single-column keys, never merged into a composite one: only one of them has to hold, so merging would demand more of the data than the query does.

## Reusing the data distribution of a real database

Setting `--null-freq` and `--values-freq-map` by hand means knowing the shape of
the production data in the first place. Postgres already measured it: `pg_stats` holds
a `null_frac`, a `most_common_vals` and a `most_common_freqs` for every analyzed
column, and those three are exactly what the two options take.

`export-stat` prints the command that dumps them. It reads nothing but `pg_stats`,
so it is safe to hand over to whoever has access to the database being copied:

```
random-data-load export-stat --engine=pg --query="select o.total from customers c join orders o on c.id = o.customer_id" --database=shop
```

```
# Reads pg_stats and writes nothing. Run it on the database whose data
# distribution you want to reproduce, then pass the file to:
#   random-data-load run --stat-file=pg_stats.json ...
psql -X -q -A -t -d shop -f - > pg_stats.json <<'SQL'
SELECT coalesce(json_agg(s), '[]'::json)
  FROM (SELECT schemaname, tablename, attname, null_frac,
               (most_common_vals::text::text[]) AS most_common_vals,
               most_common_freqs
          FROM pg_stats
         WHERE schemaname = 'public'
           AND lower(tablename) IN ('customers', 'orders')
           AND lower(attname) IN ('c', 'customer_id', 'customers', 'id', 'o', 'orders', 'total')) s;
SQL
```

The dump is narrowed down to the tables and columns the `--query` uses, the same
whitelist that decides which fields get generated. Without a `--query`, or with one
selecting a `*`, it covers every column of the tables instead. `--max-common-vals`
caps how many common values each column contributes, since postgres stores up to
`default_statistics_target` of them.

Feeding it back needs nothing else, `--table` or `--query` aside:

```
random-data-load run --engine=pg --database=shop --query="..." --rows=100000 --stat-file=pg_stats.json
```

A few things worth knowing:

- **the dump only carries statistics, never a row**. `most_common_vals` does hold real
  column values, though, so it is production data and should be treated as such
- values are matched to a table and a column of the run, case-insensitively. Anything
  the run does not insert into is ignored
- **a foreign key column keeps its skew, not its values.** `most_common_vals` for such a
  column holds the source database's own parent ids, and this run's parent was filled
  with ids of its own making, so inserting them would point the key at rows that do not
  exist. The frequencies beside them are kept instead, and reproduced by pointing that
  share of the child's rows at one parent row each — which is the figure that matters,
  since postgres reads `most_common_freqs[1]` of the inner join column to size a hash
  join's build side. The share is reduced by what the relationship's own sampling
  contributes on its own, so the result lands on what was measured rather than above it.
  Only single-column keys: the common values of one column of a composite key say how
  often that column repeats, not how often the pair does
- `--null-freq`, `--values-freq-map` and `--query-param-freq` win. Each of them is
  an instruction and the export is a measurement, so setting one is how you override
  what the export says about a value. In particular `--query-param-freq` is how you
  force a query to return rows whatever the export measured
- **`--query-param-freq` defaults to 0**, so by default the export is the only thing
  speaking for a column. Inserting a literal on 10% of the rows because a query mentions
  it is a selectivity nobody measured, and it used to win over the measured one:
  `status='cancelled'` at 10% instead of 3.98% is a sequential scan where the reported
  side had a bitmap scan
- a value is never counted twice, whichever of them it came from
- a column postgres recorded no NULL for gets none, rather than falling back to
  `--null-freq`
- `null_frac` is scaled up before use. A row is drawn as NULL first and then
  overwritten when a common value is drawn, so a column whose values cover 60% of its
  rows only keeps its NULLs on the other 40%. What ends up in the generated table is
  the `null_frac` that was measured
- frequencies that add up to more than 1 are warned about, and NULL then takes
  whatever share is left

`--engine=mysql` is refused for now rather than exporting something unusable:
`information_schema.COLUMN_STATISTICS` only holds histograms, and only for the
columns someone explicitly ran `ANALYZE TABLE ... UPDATE HISTOGRAM ON` against. On
MySQL, set the frequencies by hand with `--null-freq` and `--values-freq-map`.

## Aiming a table at a row width or a page count

Row width decides how many rows fit in a page, page count decides what a sequential
scan costs, and scan costs decide the plan — including whether postgres parallelises
at all. Solving for a width by hand is a load, a measurement, an adjustment and a
reload, and it is usually the longest loop in a reproduction.

```
random-data-load run --engine=pg --database=shop --table=orders --rows=3600000 \
    --target-relpages=44053
```

```
INF aiming orders at 44053 pages: 3600000 rows over 44053 pages is 96 bytes per row
INF orders comes out at 41 bytes per row on its own; filling shipping_address=61,
    note=1, to reach 96, which lands on 96
```

Either end of the same target can be asked for:

- `--target-bytes-per-row=N` is the average width of a row's column values, the figure
  a plan's `width=` is built from and the one `verify` reads back from the catalog
- `--target-relpages=N` is the page count, which `--rows` and postgres' page layout
  turn into a width. Postgres only: InnoDB organises a table by its primary key and
  reports a size it sampled rather than counted, so the same arithmetic would not mean
  anything there

Both take a value per table, the same way the sampler tuning does:
`--target-bytes-per-row="97;order_items=24"`.

How it gets there: before the run starts, a few hundred rows are generated and measured
to find out how wide a row comes out on its own, and the difference is written into the
columns holding free text — `char`, `varchar`, `text` and `blob` columns this run
generates itself. Each one's share is proportional to how wide it already is, so a table
whose free text is one short label and one long description keeps that shape. A column
that cannot take its share is pinned at what it holds and its remainder goes to the
others. The target works in both directions: a table that comes out wider than asked for
has those columns shortened.

Worth knowing:

- a page holds a whole number of rows and a tuple is a whole number of alignment
  boundaries wide, so **not every page count is reachable**. 20,000 rows fit in 409
  pages or in 434, and in nothing between; the run says which one it landed on
- a column sampled from a parent, or pinned by `--values-freq-map`, `--stat-file` or a
  query literal, **holds what it was given** and is not used as filler
- the filler is random rather than repeated, because postgres compresses a value before
  deciding whether to store it out of line, and repeated padding compresses to nothing
- past roughly 2000 bytes per row postgres pushes the widest column out of line into a
  TOAST table and the heap stops growing. The run warns and names
  `ALTER TABLE ... ALTER COLUMN ... SET STORAGE PLAIN`, which keeps it in the heap
- a table with no free-text column has no room to grow into, and is warned about rather
  than silently left as it was
- the width model is postgres': fixed-width types count what their type takes, variable
  ones count their length plus a header, and per-column alignment padding is not
  modelled. On MySQL the filling still happens, the arithmetic is only approximate

## Checking a run against the target

Loading the data is half of a reproduction; the other half is showing that what was
loaded matches what was asked for. `verify` reads the filled database back and prints
the two side by side:

```
random-data-load verify --engine=pg --database=shop --query="..." plan.txt \
    --rows="customers=200000;orders=3600000" --stat-file=pg_stats.json
```

```
Rows
  public.customers                             reported       200000   generated       200000    +0.0%   ok
  public.orders                                reported      3600000   generated      3600000    +0.0%   ok

Pages
  public.orders                                reported        44053   generated        44121    +0.2%   ok

Selectivity
  public.orders.status = cancelled             reported       0.0398   generated       0.0401    +0.8%   ok

Value frequency
  public.orders.status = shipped               reported       0.5055   generated       0.5061    +0.1%   ok

Every figure with a target sits within 0.0500 of it.
```

It takes the same inputs the run took, so the expectations are the ones the run was
given rather than a second set written by hand:

- the target EXPLAIN, the same file `explain-stat` reads, as a positional argument. Its
  row counts, page counts, selectivities and distinct counts become targets
- `--stat-file`, whose `null_frac` and `most_common_freqs` become targets too.
  `--max-common-vals` caps how many values of each column are checked
- `--rows`, for the sizes a plan cannot reveal on its own

A figure the reported side never gave is still printed, marked `no target`: what a run
actually produced is worth reading on its own. `--tolerance` sets how far a figure may
sit from its target before it is called `OFF`, and `--strict` turns any `OFF` into a
non-zero exit status, for a script that should stop there.

Two lines are printed but never fail a run. The row width compares the plan's `width=`,
which counts only the columns that node outputs, against the catalog's, which counts
every column of the row. The row estimate holds the counted rows against what the
planner believes the table holds, which says whether the statistics are fresh rather
than whether the data is right.

Page and row counts come from the catalog, which holds nothing at all for a table
filled a moment ago, so `verify` runs an `ANALYZE` first. `--no-analyze` leaves the
statistics alone if something else already collected them.

## Skipping fields that are not relevant to the query
When using --query, `random-data-load` will avoid generating or sampling fields that are not necessary for the query to run.
It can be disabled with --no-skip-fields.
It will also disable itself if it encounter any * , since the full length of the row would have consequences on the query execution. 


## Better field generation based on field names

Very, very minimal for now, based on simple regexes.
```
	emailRe     = regexp.MustCompile(`email`)
	firstNameRe = regexp.MustCompile(`first.*name`)
	lastNameRe  = regexp.MustCompile(`last.*name`)
	nameRe      = regexp.MustCompile(`name`)
	phoneRe     = regexp.MustCompile(`phone`)
	ssn         = regexp.MustCompile(`ssn`)
	zipRe       = regexp.MustCompile(`zip`)
	colorRe     = regexp.MustCompile(`color`)
	ipAddressRe = regexp.MustCompile(`^ip.*(?:address)*`)
	addressRe   = regexp.MustCompile(`address`)
	stateRe     = regexp.MustCompile(`state`)
	cityRe      = regexp.MustCompile(`city`)
	countryRe   = regexp.MustCompile(`country`)
	genderRe    = regexp.MustCompile(`gender`)
	urlRe       = regexp.MustCompile(`url`)
	domainre    = regexp.MustCompile(`domain`)
	productName = regexp.MustCompile(`product`)
	description = regexp.MustCompile(`description`)
	feature     = regexp.MustCompile(`feature`)
	material    = regexp.MustCompile(`material`)
	currency    = regexp.MustCompile(`currency`)
	company     = regexp.MustCompile(`company`)
	language    = regexp.MustCompile(`language`)
```

They will use an associated gofakeit generator, https://github.com/brianvoe/gofakeit


## Supported fields:

|Field type|Generated values|
|----------|----------------|
|bool|false ~ true|
|tinyint|0 ~ 0xFF|
|smallint|0 ~ 0XFFFF|
|mediumint|0 ~ 0xFFFFFF|
|int - integer|0 ~ 0xFFFFFFFF|
|bigint|0 ~ 0xFFFFFFFFFFFFFFFF|
|float|0 ~ 1e8|
|decimal(m,n)|0 ~ 10^(m-n)|
|double|0 ~ 1000|
|char(n)|up to n random chars|
|varchar(n)|up to n random chars|
|date|between --min-generated-time and --max-generated-time|
|datetime|between --min-generated-time and --max-generated-time|
|timestamp|between --min-generated-time and --max-generated-time|
|time|00:00:00 ~ 23:59:59|
|year|Current year - 1 ~ current year|
|tinyblob|up to 100 chars random paragraph|
|tinytext|up to 100 chars random paragraph|
|blob|up to --max-text-size chars random paragraph|
|text|up to --max-text-size chars random paragraph|
|mediumblob|up to --max-text-size chars random paragraph|
|mediumtext|up to --max-text-size chars random paragraph|
|longblob|up to --max-text-size chars random paragraph|
|longtext|up to --max-text-size chars random paragraph|
|enum|A random item from the valid items list|
|set|A random item from the valid items list|
|uuid|A random uuid v4, or v7 with --uuid-version=7|
|json - jsonb|A small generated document. It bears no resemblance to what production stores, only a comparable width|

Valuable types currently not implemented:
- Geospatial
- Vectors
- Arrays, and the postgres network, range and money types

A column of a type that cannot be generated is left out of the INSERT entirely, so the engine gives it its default. This is now said out loud rather than left to be discovered:
- nullable, or holding a default: a warning names the column and its type. The run works, and the column ends up entirely NULL or entirely its default, so the rows are narrower than the ones being reproduced.
- NOT NULL with no default: the run is refused before a single row is written, --dry-run included, since the engine could only reject every insert.

A column can be left out of a run on purpose by naming the columns to fill in a --query.

## How to download the precompiled binaries

There are binaries available for each version for Linux and Darwin. You can find compiled binaries for each version in the releases tab:

https://github.com/Percona-Lab/random-data-load/releases

## To do
General:
- [x] better datetime random generation. It should be flexible over its range
- [x] use more gofakeit generators with regexes to generate "legit" data when possible
- [ ] helpers to get schema (generate pgdump/mysqldump commands, get index stats, ...)
- [x] protect against foreign key cycles. Both explicits and implicits (avoid generating implicits that would end up causing loops)
- [x] detect selfpointing foreign keys 
- [x] using --values-freq-map to make query parameters work

Sampling:
- [x] normal law through box-muller, select sqrt(-2*log(random()))*sin(2*pi()*random());
- [x] pareto laws
- [ ] have some graph to show --coin-flip-percent with --bulk-size

Stepping stones to fully reproduce cardinalities:
- [x] incorporating arbitrary values with fixed frequency into the bulk inserts
- [x] table-per-table override for --rows, --null-frequency
- [ ] coin-flip-percent per relationship basis. Current thought: adding it to --binomial this way --binomial="parent=child:70" to set the coinflip to 70 for this link
- [ ] parse col/index stats (cardinality + most_common_elems + most_common_freqs for postgres, cardinalities for MySQL)
- [ ] estimate/decide sampling method+tuning based on stats

Without clear plan:
- [x] More random algorithms (as of now, no good implementations has been found for pareto that wouldn't provoke huge runtime and/or huge memory consumption, unless implemented fields are restricted to integers)
- [ ] guessing joins on subqueries/cte. Joins wouldn't be based on columns, but on expressions
- [ ] be able to "suplement" existing foreign keys with additional columns ?

## Version history

#### Unreleased
- parent keys of every type can be sampled, `uuid` and `numeric` included, instead of taking the process down with a nil scan destination
- a sampled `decimal`/`numeric` keeps every digit the database wrote, and a sampled date is written back in a form the engine reads back as the same instant
- a run that cannot fill a column now fails with a message naming the table and the reason, and a non-zero exit status, where it used to panic and exit 0
- --coin-flip-percent, --normal-mean and --normal-stddev are measured against the parent table being sampled rather than against --rows, so a small parent feeding a large child works with the defaults
- --sequential wraps around the parent instead of running past its last row, making it a round robin once the parent is exhausted; it no longer panics when asked for more children than the parent has rows
- a value given a frequency by --values-freq-map is no longer counted a second time when the --query mentions it too, which used to add the two frequencies together
- json and jsonb columns are generated instead of being dropped from the INSERT
- a column of a type that cannot be generated is reported: warned about when it is nullable or has a default, and refused up front when it is NOT NULL without one, in --dry-run too
- an empty parent table is reported by name instead of failing with an empty sample
- sampled text values are escaped, so a parent key holding a quote no longer breaks the insert
- foreign keys are now guessed through subqueries and CTEs, projected down onto the real tables they read, including UNION branches, recursive CTEs, and columns renamed by an alias or a CTE column list
- foreign keys are guessed from implicit JOINs written in the WHERE clause, from correlated EXISTS subqueries, and from IN (subquery) semi-joins
- a multi-column JOIN condition now produces a single composite foreign key instead of one key per column, so a child row no longer mixes columns from different parent rows
- only equality conditions produce a foreign key; range and negated conditions no longer invent one
- a JOIN condition that cannot be traced to real columns is now reported with a warning naming the condition, instead of being dropped silently
- fixed a crash on schema-qualified columns in a JOIN condition, e.g. `ON public.orders.order_id = oi.order_id`
- fixed a query-guessed foreign key being added a second time when the schema already declared it as part of a composite key, which produced an INSERT listing a column twice
- columns read only inside a CTE are no longer left out of the generated fields
- `run --stat-file` reads a column statistics export and sets the null and value frequencies from `null_frac`, `most_common_vals` and `most_common_freqs`
- new `export-stat` subcommand, printing the command that exports those statistics for the tables and columns a `--query` uses. Only `--engine=pg` for now
- injected values are now escaped before reaching the INSERT, so a value holding a quote no longer breaks the statement
- `--query-param-freq=0` no longer registers the query literals at a frequency of zero, it now leaves them out entirely
- new `verify` subcommand, reading a filled database back and printing its row counts, page counts, selectivities, distinct counts and column statistics next to the reported figures they were meant to match
- `run --target-bytes-per-row` and `run --target-relpages` aim a table at a row width or a page count, distributing the difference over the columns holding free text, so a load-measure-adjust cycle becomes one flag
- a unique key whose columns come from several foreign keys is filled from all of them at once, walking the combinations their parents can make, instead of each key walking its own parent and the pair repeating as soon as the shortest walk came round; asking for more rows than those parents can make combinations is refused up front
- a run whose tables point foreign keys at tables it does not fill is refused before anything is written, in one message naming the whole closure, instead of failing part way through with some tables already loaded; `--fill-fk-parents` adds those tables to the run instead
- `--stat-file` no longer tries to insert the source database's parent ids into a foreign key column, which pointed it at rows that do not exist; the key's measured skew is reproduced instead, by sampling that share of the child's rows from one parent row each
- `--query-param-freq` now defaults to 0 and is an override rather than a guess: nothing is inserted because a query mentions it unless you ask, and asking overrides what `--stat-file` measured for those values. A predicate no row will match is named in a warning, so an empty result explains itself

#### 0.2.3
- NULL and/or fixed values can be injected at tunable rates
- --rows can be overriden per tables 
- improved virtual join handling to enable columns used for many foreign keys
- query parameters are being inserted at tunable frequencies so that query can work as is 
- protection against circular dependencies
- self-referencing tables handling through splitting the tables in two. Half the table will reference the other half


#### 0.2.0
- Support for postgres
- parallelism
- bool types
- uniform foreign key patterns
- skipping unecessary columns and backfilling missing foreign keys through query analysis

#### 0.1.10
- Fixed argument validations
- Fixed ~/.my.cnf loading

#### 0.1.10
- Fixed connection parameters for MySQL 5.7 (set driver's AllowNativePasswords: true)

#### 0.1.9
- Added support for bunary and varbinary columns
- By default, read connection params from ${HOME}/.my.cnf

#### 0.1.8 
- Fixed error for triggers created with MySQL 5.6
- Added Travis-CI
- Code clean up

#### 0.1.7 
- Support for MySQL 8.0
- Added --print parameter 
- Added --version parameter
- Removed qps parameter

#### 0.1.6 
- Improved generation speed (up to 50% faster)
- Improved support for TokuDB (Thanks Agustin Gallego)
- Code refactored
- Improved debug logging
- Added Query Per Seconds support (experimental)

#### 0.1.5 
- Fixed handling of NULL collation for index parser

#### 0.1.4
- Fixed handling of time columns
- Improved support of GENERATED columns

#### 0.1.3
- Fixed handling of nulls

#### 0.1.2
- New table parser able to retrieve all the information for fields, indexes and foreign keys constraints.
- Support for foreign keys constraints
- Added some tests

#### 0.1.1
- Fixed random data generation

#### 0.1.0
- Initial version







