# Random data generator for MySQL and PostgreSQL
Forked from https://github.com/Percona-Lab/mysql_random_data_load

This tool aims to produce a quick working environment to reproduce a query execution behavior in order to optimize it.
It is meant for cases where we cannot access real data, only schema and cardinalities. 

Based on the table(s) schema and a query, it will generate random data with respect to fields, foreign keys defined in databases, foreign keys infered from the query pattern, (plan: from existing cardinalities and distributions). 

Notice:
This is early stage

## Usage
`random-data-load run --engine=(mysql|pg) --rows=INT-64 (--query=SELECT ...|--table=table_name) [options...]`

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
|date|NOW() - 1 year ~ NOW()|
|datetime|NOW() - 1 year ~ NOW()|
|timestamp|NOW() - 1 year ~ NOW()|
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

Valuable types currently not implemented:
- JSONs
- Geospatial
- Vectors

## Options
|Option|Description|
|------|-----------|
|--engine|mysql/pg|
|--host|Host name/ip|
|--user|Username|
|--password|Password|
|--port|Port number|
|--bulk-size|Number of rows per INSERT statement (Default: 1000)|
|--workers|how many workers to spawn. Only the random generation and sampling are parallelized. Insert queries are executed one at a time (Default: 3)|
|--table|Table to insert to. When using --query, --table will be used to restrict the tables to insert to.|
|--query|Providing a query will analyze its schema usage, insert recursively into tables, and identify implicit joins|
|--default-relationship|Will define the default foreign-key relationship to apply. Possible values: binomial,sequential. The default relation can be overriden with other parameters --binomial or --sequential|
|--binomial|Defines a 1-N foreign key relationships using repeated coin flips. Postgres' tablesamples Bernouilli or mysql RAND() < 0.1 (can be tuned with --coin-flip-percent). Format should be "parent_table=child_table". E.g: --binomial="customers=orders;orders=items"|
|--coin-flip-percent|When used with --binomial, it will set the likeliness of each rows to be sampled or not. 10 would mean each rows have only 10%% chance to be selected when sampling a parent table. Using large values will favor hot rows: the coin flips are done with a table full scan, with a limit set at --bulk-size, so with a large percent chance most of the time the first rows will be selected. No effects when used with --sequential (Default: 1)|
|--sequential|Defines a sequential foreign key links relationships. Format should be "parent_table=child_table". E.g: --sequential="citizens=ssns"|
|--add-foreign-keys|Add foreign keys, if they are not explicitely created in the table schema. The format must be parent_table.col1=child_table.col2. It can complement the foreign keys guessed from the --query, or be used to manually define foreign keys when using --no-fk-guess too. Example --add-foreign-keys="customers.id=purchases.customer_id;purchases.id=items.purchase_id"|
|--no-fk-guess|Do not try to guess foreign keys from the --query missing in the schema. When a query is provided, it will analyze the expected JOINs and try to respect dependencies even when foreign keys are not explicitely created in the database objects. This flag will make the tool stick to the constraints defined in the database only, unless you add foreign keys manually with --add-foreign-keys.|
|--no-skip-fields|Disable field whitelist system. When using a --query, it will get the list of fields being used as a whitelist in order to generate the minimal sets of fields required, unless --no-skip-fields is being used or any * has been found.|
|--null-frequency|Define how frequent nullable fields should be NULL|
|--quiet|Do not print progress bar|
|--dry-run|Print queries to the standard output instead of inserting them into the db|
|--debug|Show some debug information|
|--pprof|Generate pprof trace at --cpu-prof-path. Also opens port 6060 for pprof go tool|
|--version|Show version and exit|

## Foreign keys support
If a field has Foreign Keys constraints, `random-data-load` will get samples from the referenced tables in order to insert valid values for the field.  
To enforce orders, an arbitrary 'ORDER BY 1' is made. This is so that --sequential can create 1-1 relationship, and to better master the eventual distribution of --binomial.

Composites foreign keys are supported.
With very low chances to sample rows, we might sample too little. The tool will loop until it sampled enough rows to fill the next bulk insert.

**1.** sequential relationships will sample with LIMIT and OFFSET:  
```
SELECT <field[, field2]> FROM <referenced schema>.<referenced table> ORDER BY 1 LIMIT <--bulk-size> OFFSET y
```
This isn't the fastest method but it works for every types. The value of the current OFFSET is protected by mutex to prevents frequent duplicates. 

**2.** binomial relations will sample differently between postgres and mysql

**2.1** For postgres it relies on TABLESAMPLE
```
SELECT <field[, field2]> FROM <referenced schema>.<referenced table> TABLESAMPLE BERNOUILLI (<--coin-flip-percent>) ORDER BY 1 LIMIT <--bulk-size>
```

**2.2** For mysql, it relies on RAND()
```
SELECT <field[, field2]> FROM <referenced schema>.<referenced table> WHERE rand() < (<--coin-flip-percent>/100) ORDER BY 1 LIMIT <--bulk-size>
```

## Guessing implicit foreign keys from queries
If no foreign keys are explicitely defined in the schema, but the query is using JOINs with a "ON" clause, `random-data-load` will infer the foreign keys and insert valid values so that JOINs work.
Can be disabled with --no-fk-guess

An estimation can be made using:
```
random-data-load query --query="$(cat huge_select.sql)"
``` 

It will skip guessing foreign keys for those cases:
- JOINs relying on subqueries instead of tables
- JOINs made implicitely without JOIN keywords or "ON" clauses
- (limitation) JOINs having its ON clause between parenthesis are currently thought to be subqueries and are skipped
- JOINs conditions using ambiguous columns, without expliciting to what table it belongs. Example `FROM x JOIN y ON apple=pear` instead of `FROM x JOIN y ON x.apple=y.pear`

## Skipping fields that are not relevant to the query
When using --query, `random-data-load` will avoid generating or sampling fields that are not necessary for the query to run.
It can be disabled with --no-skip-fields.
It will also disable itself if it encounter any * , since the full length of the row would have consequences on the query execution. 

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
$ time ./random-data-load run --engine=pg --host=127.0.0.1 --user=sbtest --password=sbtest --database=postgres --port=5434 --bulk-size=4500 --rows=500000 --default-relationship=binomial --coin-flip-percent=1  --query="select sum(p.price), count(oi.*) from orders o join order_items oi on o.order_id=oi.order_id join products p on p.id = oi.product_no where o.currency='EUR';" 
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

postgres=# explain analyze select sum(p.price), count(oi.*) from orders o join order_items oi on o.order_id=oi.order_id join products p on p.id = oi.product_no where o.currency='EUR';
                                                                          QUERY PLAN                                                                           
---------------------------------------------------------------------------------------------------------------------------------------------------------------
 Finalize Aggregate  (cost=13292.70..13292.71 rows=1 width=40) (actual time=146.168..158.266 rows=1 loops=1)
   ->  Gather  (cost=13292.48..13292.69 rows=2 width=40) (actual time=145.843..158.250 rows=3 loops=1)
         Workers Planned: 2
         Workers Launched: 2
         ->  Partial Aggregate  (cost=12292.48..12292.49 rows=1 width=40) (actual time=141.517..141.521 rows=1 loops=3)
               ->  Nested Loop  (cost=6840.79..12289.63 rows=568 width=112) (actual time=42.692..141.046 rows=1077 loops=3)
                     ->  Parallel Hash Join  (cost=6840.37..11952.90 rows=568 width=184) (actual time=42.600..126.572 rows=1077 loops=3)
                           Hash Cond: (oi.order_id = o.order_id)
                           ->  Parallel Seq Scan on order_items oi  (cost=0.00..4814.67 rows=113467 width=188) (actual time=0.055..58.369 rows=166667 loops=3)
                           ->  Parallel Hash  (cost=6836.85..6836.85 rows=281 width=4) (actual time=42.157..42.159 rows=1059 loops=3)
                                 Buckets: 4096 (originally 1024)  Batches: 1 (originally 1)  Memory Usage: 216kB
                                 ->  Parallel Seq Scan on orders o  (cost=0.00..6836.85 rows=281 width=4) (actual time=0.090..36.764 rows=1059 loops=3)
                                       Filter: ((currency)::text = 'EUR'::text)
                                       Rows Removed by Filter: 165608
                     ->  Index Scan using products_pkey on products p  (cost=0.42..0.59 rows=1 width=27) (actual time=0.012..0.012 rows=1 loops=3231)
                           Index Cond: ((id)::text = (oi.product_no)::text)
 Planning Time: 0.365 ms
 Execution Time: 158.359 ms
(18 rows)

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


## How to download the precompiled binaries

There are binaries available for each version for Linux and Darwin. You can find compiled binaries for each version in the releases tab:

https://github.com/Percona-Lab/random-data-load/releases

## To do
General:
- [ ] better datetime random generation. It should be flexible over its range
- [x] use more gofakeit generators with regexes to generate "legit" data when possible
- [ ] helpers to get schema (generate pgdump/mysqldump commands, get index stats, ...)
Stepping stones to fully reproduce cardinalities:
- [ ] incorporating arbitrary values with fixed frequency into the bulk inserts, so that query parameters work.
- [ ] table-per-table override for --rows, --null-frequency
- [ ] coin-flip-percent per relationship basis. Current thought: adding it to --binomial this way --binomial="parent=child:70" to set the coinflip to 70 for this link
- [ ] parse col/index stats (cardinality + most_common_elems + most_common_freqs for postgres, cardinalities for mysql)
Without clear plan:
- [ ] More random algorithms (as of now, no good implementations has been found for pareto that wouldn't provoke huge runtime and/or huge memory consumption, unless implemented fields are restricted to integers)

## Version history

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




