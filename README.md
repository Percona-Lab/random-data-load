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
|--default-relationship|Will define the default foreign-key relationship to apply. Possible values: binomial,1-1. The default relation can be overriden with other parameters --binomial or --1-1|
|--binomial|Defines a 1-N foreign key relationships using repeated coin flips. Postgres' tablesamples Bernouilli or mysql RAND() < 0.1 (can be tuned with --coin-flip-percent). E.g: --binomial="customers=orders;orders=items"|
|--coin-flip-percent|When used with --binomial, it will set the likeliness of each rows to be sampled or not. 10 would mean each rows have only 10%% chance to be selected when sampling a parent table. Using large values will favor hot rows: the coin flips are done with a table full scan, with a limit set at --bulk-size, so with a large percent chance most of the time the first rows will be selected. No effects when used with --1-1 (Default: 10)|
|--1-1|Defines a 1-1 foreign key links relationships. E.g: --1-1="citizens=ssns"|
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

Composites foreign keys are supported.
With very low chances to sample rows, we might sample too little. The tool will loop until it sampled enough rows to fill the next bulk insert.

**1.** 1-1 relationships will sample with LIMIT and OFFSET:  
```
SELECT <field[, field2]> FROM <referenced schema>.<referenced table> LIMIT <--bulk-size> OFFSET y
```
This isn't the fastest method but it works for every types. The value of the current OFFSET is protected by mutex to prevents frequent duplicates, however there are currently no ORDER BYs to truly secure against re-using samples. 

**2.** binomial relations will sample differently between postgres and mysql

**2.1** For postgres it relies on TABLESAMPLE
```
SELECT <field[, field2]> FROM <referenced schema>.<referenced table> TABLESAMPLE BERNOUILLI (<--coin-flip-percent>) LIMIT <--bulk-size>
```

**2.2** For mysql, it relies on RAND()
```
SELECT <field[, field2]> FROM <referenced schema>.<referenced table> WHERE rand() < (<--coin-flip-percent>/100) LIMIT <--bulk-size>
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
```
```

## How to download the precompiled binaries

There are binaries available for each version for Linux and Darwin. You can find compiled binaries for each version in the releases tab:

https://github.com/Percona-Lab/random-data-load/releases

## To do
General:
- [ ] better datetime random generation. It should be flexible over its range
- [ ] use more gofakeit generators with regexes to generate "legit" data when possible
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




