package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/ory/dockertest"
)

var toolExecutable = "./random-data-load"

var testsdb map[string]struct {
	resource *dockertest.Resource
	db       *sql.DB
	port     string
}

func TestMain(m *testing.M) {

	// uses a sensible default on windows (tcp/http) and linux/osx (socket)
	// DOCKER_HOST=unix:///run/user/1000/docker.sock go test .
	pool, err := dockertest.NewPool("")
	if err != nil {
		log.Panicf("Could not construct pool: %s", err)
	}

	err = pool.Client.Ping()
	if err != nil {
		log.Panicf("Could not connect to Docker: %s", err)
	}

	pgresource, err := pool.Run("postgres", "17", []string{"POSTGRES_PASSWORD=dockertest", "POSTGRES_USER=dockertest", "POSTGRES_DB=test"})
	if err != nil {
		log.Panicf("Could not start pg resource: %s", err)
	}
	mysqlresource, err := pool.Run("mysql", "8.0", []string{"MYSQL_ROOT_PASSWORD=dockertest", "MYSQL_PASSWORD=dockertest", "MYSQL_DATABASE=test", "MYSQL_USER=dockertest"})
	if err != nil {
		log.Panicf("Could not start mysql resource: %s", err)
	}
	/*	defer func() {
			for _, resource := range []*dockertest.Resource{pgresource, mysqlresource} {
				if err := pool.Purge(resource); err != nil {
					log.Panicf("Could not purge resource: %s", err)
				}
			}
		}()
	*/

	var pgdb *sql.DB
	if err = pool.Retry(func() error {
		pgdb, err = sql.Open("postgres", fmt.Sprintf("postgres://dockertest:dockertest@%s/test?sslmode=disable", pgresource.GetHostPort("5432/tcp")))
		if err != nil {
			return err
		}
		return pgdb.Ping()
	}); err != nil {
		log.Panicf("Could not connect to pg docker: %s", err)
	}

	// The image only grants the test user the `test` database, and the catalog
	// only ever shows a user the objects it has rights on. A fixture giving a
	// constraint name a namesake in a second database needs both to create it
	// and to have the tool see it. Global privileges only reach a client on
	// its next connection, so this comes before the test user connects.
	var mysqlrootdb *sql.DB
	if err = pool.Retry(func() error {
		mysqlrootdb, err = sql.Open("mysql", fmt.Sprintf("root:dockertest@(localhost:%s)/test", mysqlresource.GetPort("3306/tcp")))
		if err != nil {
			return err
		}
		return mysqlrootdb.Ping()
	}); err != nil {
		log.Panicf("Could not connect to mysql docker as root: %s", err)
	}
	if _, err = mysqlrootdb.Exec("GRANT ALL PRIVILEGES ON *.* TO 'dockertest'@'%'"); err != nil {
		log.Panicf("Could not grant privileges to the mysql test user: %s", err)
	}
	mysqlrootdb.Close()

	mysqldb, err := sql.Open("mysql", fmt.Sprintf("dockertest:dockertest@(localhost:%s)/test?multiStatements=true", mysqlresource.GetPort("3306/tcp")))
	if err != nil {
		log.Panicf("Could not connect to mysql docker: %s", err)
	}
	if err = mysqldb.Ping(); err != nil {
		log.Panicf("Could not connect to mysql docker: %s", err)
	}

	testsdb = map[string]struct {
		resource *dockertest.Resource
		db       *sql.DB
		port     string
	}{
		"pg": struct {
			resource *dockertest.Resource
			db       *sql.DB
			port     string
		}{
			resource: pgresource,
			db:       pgdb,
			port:     pgresource.GetPort("5432/tcp"),
		},
		"mysql": struct {
			resource *dockertest.Resource
			db       *sql.DB
			port     string
		}{
			resource: mysqlresource,
			db:       mysqldb,
			port:     mysqlresource.GetPort("3306/tcp"),
		},
	}

	// run tests
	code := m.Run()

	if code != 0 && keepDB() {
		log.Printf("Keeping database running because tests failed and KEEP_DB=1")
		return
	}
	for _, resource := range []*dockertest.Resource{pgresource, mysqlresource} {
		if err := pool.Purge(resource); err != nil {
			log.Panicf("Could not purge resource: %s", err)
		}
	}
}

func TestRun(t *testing.T) {

	tests := []struct {
		name       string
		checkQuery string // used to check if the generated result seems appropriate
		inputQuery string // applicative query we want to optimize
		engines    []string
		tables     []string
		cmds       [][]string
		expectErr  string // the run has to fail, and to say this much
	}{
		{
			name:       "basic",
			checkQuery: "select count(*) = 10 from t1;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=10", "--table=t1"}},
		},

		{
			name:       "pk_bigserial",
			checkQuery: "select count(*) = 100 from t1;",
			engines:    []string{"pg"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}},
		},
		{
			name:       "pk_identity",
			checkQuery: "select count(*) = 100 from t1 where id < 101;",
			engines:    []string{"pg"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}},
		},
		{
			name:       "pk",
			checkQuery: "select count(*) = 100 from t1;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}},
		},
		{
			name:       "pk_auto_increment",
			checkQuery: "select count(*) = 100 from t1 where id < 101;",
			engines:    []string{"mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}},
		},

		{
			name:       "pk_varchar",
			checkQuery: "select count(*) = 100 from t1;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}},
		},

		{
			name:       "bool",
			checkQuery: "select (count(*) = 100) and (sum(CASE WHEN c1 THEN 1 ELSE 0 END) between 1 and 99) from t1;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}},
		},

		{
			name:       "timestamp",
			checkQuery: "select (count(*) = 100) and (sum(CASE WHEN c1 between '2015-07-02' and '2020-09-08' THEN 1 ELSE 0 END) = 100) from t1;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1", "--min-generated-time=2015-07-02T00:00:00Z", "--max-generated-time=2020-09-08T00:00:00Z", "--null-freq=0"}},
		},

		{
			name:       "fk_uniform",
			checkQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=sequential"}},
		},

		// not a great test for now, but we want some matches, but not every lines matched
		{
			name:       "fk_binomial",
			checkQuery: "select count(distinct t1.id) between 1 and 99 from t1 join t2 on t1.id = t2.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=binomial", "--coin-flip-percent=60"}},
		},
		{
			name:       "fk_pareto",
			checkQuery: "select count(distinct t1.id) between 1 and 99 from t1 join t2 on t1.id = t2.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=pareto"}},
		},
		{
			name:       "fk_normal",
			checkQuery: "select count(distinct t1.id) between 1 and 99 from t1 join t2 on t1.id = t2.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=normal"}},
		},

		// The normal law reaches past the end of the table, so half of these
		// row numbers have to be drawn again. Testing the same draw again
		// could only give the same row number back, and looped forever.
		{
			name:       "fk_normal_outside_table",
			checkQuery: "select count(distinct t1.id) between 1 and 99 from t1 join t2 on t1.id = t2.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=normal", "--normal-mean=100", "--normal-stddev=50"}},
		},

		// 5% of 1000 will end up being 50, but we need 100 samples per chunks and t1_id has NOT NULL so it has to loop to get more samples
		{
			name:       "fk_binomial_looping_chunks",
			checkQuery: "select count(distinct t1.id) between 1 and 999 from t1 join t2 on t1.id = t2.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=1000", "--table=t1"}, []string{"--rows=1000", "--table=t2", "--default-relationship=binomial", "--coin-flip-percent=5", "--bulk-size=100"}},
		},

		{
			name:       "fk_multicol",
			checkQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id and t1.id2 = t2.t1_id2;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=sequential"}},
		},

		// A constraint is only identified by its name together with the table
		// holding it. Looked up by name alone, the namesake the fixture adds
		// gets its column folded into t2's constraint, which then lists t1_id
		// twice in the INSERT, and points at the namesake's parent table.
		{
			name:       "fk_shared_constraint_name",
			checkQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=sequential"}},
		},

		{
			name:       "fk_pivot_varchar_integer",
			checkQuery: "select count(*) = 100 from t1 join t3 on t1.order_id=t3.order_id join t2 on t2.id = t3.product_no;",
			inputQuery: "select sum(t2.price), count(t1.*) from t1 join t3 on t1.order_id=t3.order_id join t2 on t2.id = t3.product_no where t1.currency='EUR';",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--default-relationship=sequential"}},
		},

		{
			name:       "basic_query",
			checkQuery: "select (count(*) = 100) and (sum(CASE WHEN c2 IS NULL THEN 1 ELSE 0 END) = 100)  from t1 where c1 is not null;",
			inputQuery: "select c1 from t1;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1", "--null-freq=0"}},
		},

		{
			name:       "identifiers_skip_not_null_nodefaults",
			checkQuery: "select (count(*) = 100) and (sum(CASE WHEN c2 <> '' THEN 1 ELSE 0 END) = 100)  from t1 where c1 is not null;",
			inputQuery: "select c1 from t1;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}},
		},

		{
			name:       "identifiers_skip_not_null_defaults",
			checkQuery: "select (count(*) = 100) and (sum(CASE WHEN c2 <> 'test' THEN 1 ELSE 0 END) = 0)  from t1 where c1 is not null;",
			inputQuery: "select c1 from t1;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}},
		},

		{
			name:       "identifiers_skip_fk_multicol",
			checkQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id and t1.id2 = t2.t1_id2;",
			inputQuery: "select a1.id, a1.id2 from t1 a1 join t2 a2 on a1.id = a2.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=sequential"}},
		},
		{
			name: "fk_cascade_recursive",
			// t1 alone, t2 dep on t1, t3 dep on t2 and t4 dep on t2+t3
			checkQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id join t3 on t2.id = t3.t2_id join t4 on t3.id = t4.t3_id and t2.id = t4.t2_id;",
			inputQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id join t3 on t2.id = t3.t2_id join t4 on t3.id = t4.t3_id and t2.id = t4.t2_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--default-relationship=sequential"}},
		},
		{
			// same as above, but with the query join order reversed
			name:       "fk_cascade_recursive_reversed",
			checkQuery: "select count(*) = 100 from t4 join t2 on t2.id = t4.t2_id join t3 on t3.id = t4.t3_id and t3.t2_id = t2.id join t1 on t1.id = t2.t1_id;",
			inputQuery: "select count(*) = 100 from t4 join t2 on t2.id = t4.t2_id join t3 on t3.id = t4.t3_id and t3.t2_id = t2.id join t1 on t1.id = t2.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--default-relationship=sequential"}},
		},

		{
			name:       "fk_virtual",
			checkQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id;",
			inputQuery: "select * from t1 join t2 on t1.id = t2.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=sequential"}},
		},

		{
			// The join is written through a CTE, so it can only be found by
			// descending into the CTE body and projecting the reference down
			// onto the table it reads.
			name:       "fk_virtual_cte",
			checkQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id;",
			inputQuery: "with a as (select * from t1) select * from a join t2 on a.id = t2.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=sequential"}},
		},

		{
			name:       "fk_virtual_derived_table",
			checkQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id;",
			inputQuery: "select * from (select * from t1) a join t2 on a.id = t2.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=sequential"}},
		},

		{
			// A semi-join asks for the same value overlap as a join: t2's
			// values have to exist in t1.
			name:       "fk_virtual_semijoin",
			checkQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id;",
			inputQuery: "select * from t2 where t2.t1_id in (select t1.id from t1);",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=sequential"}},
		},

		{
			// A comma-separated FROM puts the join condition in WHERE.
			name:       "fk_virtual_where_join",
			checkQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id;",
			inputQuery: "select * from t1, t2 where t1.id = t2.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=sequential"}},
		},

		{
			// A composite key guessed from the query, with no foreign key in
			// the schema to fall back on. Both columns have to come from the
			// same parent row, which is only guaranteed if the two equalities
			// are generated as one key rather than two.
			name:       "fk_virtual_multicol",
			checkQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id and t1.id2 = t2.t1_id2;",
			inputQuery: "select * from t1 join t2 on t1.id = t2.t1_id and t1.id2 = t2.t1_id2;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=sequential"}},
		},

		{
			name:       "fk_virtual_cascade_table_per_table",
			checkQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id join t3 on t2.id = t3.t2_id join t4 on t3.id = t4.t3_id;",
			inputQuery: "select * from t1 join t2 on t1.id = t2.t1_id join t3 on t2.id = t3.t2_id join t4 on t3.id = t4.t3_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=sequential"}, []string{"--rows=100", "--table=t3", "--default-relationship=sequential"}, []string{"--rows=100", "--table=t4", "--default-relationship=sequential"}},
		},
		{
			name:       "fk_virtual_cascade_recursive",
			checkQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id join t3 on t2.id = t3.t2_id join t4 on t3.id = t4.t3_id;",
			inputQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id join t3 on t2.id = t3.t2_id join t4 on t3.id = t4.t3_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--default-relationship=sequential"}},
		},

		{
			name:       "star_query",
			checkQuery: "select (count(*) = 100) and (sum(CASE WHEN c2 IS NOT NULL THEN 1 ELSE 0 END) = 100)  from t1 where c1 is not null;",
			inputQuery: "select * from t1;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}},
		},

		{
			name:       "text_max_size",
			checkQuery: "select (count(*) = 100) from t1 where length(data) < 10;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1", "--max-text-size=9"}},
		},

		{
			name:       "uuid",
			checkQuery: "select (count(*) = 100) from t1;",
			engines:    []string{"pg"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}},
		},

		{
			name:       "enum",
			checkQuery: "select (sum(CASE WHEN possible_values = 'V1' THEN 1 ELSE 0 END) > 0) and (sum(CASE WHEN possible_values = 'V2' THEN 1 ELSE 0 END) > 0) and (sum(CASE WHEN possible_values = 'V3' THEN 1 ELSE 0 END) > 0) from t1;",
			engines:    []string{"mysql"},
			cmds:       [][]string{[]string{"--rows=300", "--table=t1"}},
		},

		{
			name:       "mixed_cases",
			checkQuery: "select count(*) = 100 from `SomeTABLEWithCase` where `COLUMN_1` is not null and `aNOTHER_COLUMN` is not null;",
			engines:    []string{"mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=SomeTABLEWithCase", "--null-freq=0"}},
		},

		{
			name:       "fk_virtual_mixed_cases",
			checkQuery: "select count(*) = 100 from `PARENT_TABLE` pT join `CHILD_TABLE` cT on pT.`ParentTableId` = cT.`ParentTableId` where pT.`pARENTTableData` is not null;",
			inputQuery: "select * from `PARENT_TABLE` pT join `CHILD_TABLE` cT on pT.`ParentTableId` = cT.`ParentTableId` where pT.`pARENTTableData` is not null;",
			engines:    []string{"mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--default-relationship=sequential", "--null-freq=0"}},
		},

		{
			name:       "fk_sbtest_pointing_to_shared_tables",
			checkQuery: "SELECT count(*) = 100 FROM t1 LEFT JOIN t2 ON t1.id = t2.id LEFT JOIN t3 ON t1.id = t3.id LEFT JOIN t4 ON t1.id = t4.id",
			inputQuery: "SELECT t1.c, t2.c, t3.c, t4.c FROM t1 LEFT JOIN t2 ON t1.id = t2.id LEFT JOIN t3 ON t1.id = t3.id LEFT JOIN t4 ON t1.id = t4.id WHERE t1.id = 49877",
			engines:    []string{"mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--default-relationship=sequential", "--null-freq=0"}},
		},

		{
			name:       "fk_sbtest_pointing_to_shared_tables_no_fk_guess_manual_fks",
			checkQuery: "SELECT count(*) = 100 FROM t1 LEFT JOIN t2 ON t1.id = t2.id LEFT JOIN t3 ON t1.id = t3.id LEFT JOIN t4 ON t1.id = t4.id",
			inputQuery: "SELECT t1.c, t2.c, t3.c, t4.c FROM t1 LEFT JOIN t2 ON t1.id = t2.id LEFT JOIN t3 ON t1.id = t3.id LEFT JOIN t4 ON t1.id = t4.id WHERE t1.id = 49877",
			engines:    []string{"mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--default-relationship=sequential", "--null-freq=0", "--no-fk-guess", "--add-fk=\"t1.id=t2.id;t1.id=t3.id;t1.id=t4.id\""}},
		},

		{
			name:       "null_map",
			checkQuery: "select (count(*) = 100000) AND (sum(CASE WHEN c1 IS NULL THEN 1 ELSE 0 END) between 19500 and 20500) AND (sum(CASE WHEN c2 IS NULL THEN 1 ELSE 0 END) between 39500 and 40500) AND (sum(CASE WHEN c3 IS NULL THEN 1 ELSE 0 END) between 89500 and 90500) from t1;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100000", "--table=t1", "--null-freq=0.2", "--null-freq-map=t1.c2=0.4;t1.c3=0.9"}},
		},

		{
			name:       "values_freq_map",
			checkQuery: "select (count(*) = 100000) AND (sum(CASE WHEN c1 = 42 THEN 1 ELSE 0 END) between 79500 and 80500) AND (sum(CASE WHEN c1 = 7 THEN 1 ELSE 0 END) between 4500 and 5500) AND (sum(CASE WHEN c2 = 'pg' THEN 1 ELSE 0 END) between 36500 and 37500) AND (sum(CASE WHEN c2 = 'mysql' THEN 1 ELSE 0 END) between 33500 and 34500) AND (sum(CASE WHEN c2 = 'other' THEN 1 ELSE 0 END) between 400 and 600) from t1;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100000", "--table=t1", "--null-freq=0", "--values-freq-map=t1.c1=42:0.8,7:0.05;t1.c2=pg:0.37,mysql:0.34,other:0.005"}},
		},

		{
			name:       "query_params",
			checkQuery: "select (count(*) = 100000) AND (sum(CASE WHEN c2 = 'it' THEN 1 ELSE 0 END) between 9500 and 10500) AND (sum(CASE WHEN c2 = 'should' THEN 1 ELSE 0 END) between 9500 and 10500) AND (sum(CASE WHEN c2 = 'work' THEN 1 ELSE 0 END) between 9500 and 10500) from t1;",
			inputQuery: "select * from t1 where t1.c2 in ('it', 'should', 'work')",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100000", "--table=t1", "--null-freq=0", "--query-param-freq=0.1"}},
		},
		{
			name:       "query_params_no_infix",
			checkQuery: "select (count(*) = 100000) AND (sum(CASE WHEN c2 = 'it' THEN 1 ELSE 0 END) between 9500 and 10500) AND (sum(CASE WHEN c2 = 'should' THEN 1 ELSE 0 END) between 9500 and 10500) AND (sum(CASE WHEN c2 = 'work' THEN 1 ELSE 0 END) between 9500 and 10500) from t1;",
			inputQuery: "select * from t1 where c2 in ('it', 'should', 'work')",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100000", "--table=t1", "--null-freq=0", "--query-param-freq=0.1"}},
		},

		{
			name:       "fk_self_referencing",
			checkQuery: "select count(*) = 500 from t1 join t1 t1_2 on t1.id = t1_2.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=1000", "--table=t1", "--default-relationship=sequential"}},
		},

		// A table the query joins to itself has no foreign key saying so, and
		// the guessed one is a loop of its own: it used to leave the run with
		// no possible insert order, sorting the tables forever.
		{
			name:       "fk_virtual_self_referencing",
			checkQuery: "select count(*) = 500 from t1 join t1 t1_2 on t1.id = t1_2.t1_id;",
			inputQuery: "select a.id, b.t1_id from t1 a join t1 b on a.id = b.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=1000", "--default-relationship=sequential"}},
		},

		// A key can only point at a key, so the parent is t1 even though the
		// query names t2 first. Read as written, t1's own primary key was
		// sampled from t2's rows instead, and took their values.
		{
			name:       "fk_virtual_join_written_backwards",
			checkQuery: "select (count(*) = 100) and (max(id) <= 100) from t1;",
			inputQuery: "select t2.id from t2 join t1 on t2.t1_id = t1.id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--default-relationship=sequential"}},
		},

		// The side a join names first means nothing, so the guess has to be
		// read the other way round when it would close a loop with a key the
		// schema already has. Read as written, t1 and t2 waited on each other
		// and the tables were sorted forever.
		{
			name:       "fk_virtual_reversed_join",
			checkQuery: "select count(*) = 100 from t2 join t1 on t2.t1_id = t1.id;",
			inputQuery: "select t2.id from t2 join t1 on t2.t1_id = t1.id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--default-relationship=sequential"}},
		},

		{
			name:       "virtual_col",
			checkQuery: "select count(*) = 100 from t1 where id = v",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}},
		},

		{
			// tests/pg/pg_stats.json is a dump as a real postgres would have
			// returned it: c1 seen with 60.5% of 42, 14.9% of 7 and 9.8% of
			// nulls, c2 with 30.2% of 'pg', 24.8% of a value holding a quote
			// and 24.9% of nulls, and c3 with no common value and no null at
			// all. Regenerating has to land back on those shares, c3 included:
			// a column postgres saw no null in must not fall back to
			// --null-freq.
			name: "pg_stats",
			checkQuery: `select (count(*) = 100000)
				AND (sum(CASE WHEN c1 = 42 THEN 1 ELSE 0 END) between 58500 and 62500)
				AND (sum(CASE WHEN c1 = 7 THEN 1 ELSE 0 END) between 13000 and 17000)
				AND (sum(CASE WHEN c1 IS NULL THEN 1 ELSE 0 END) between 8000 and 12000)
				AND (sum(CASE WHEN c2 = 'pg' THEN 1 ELSE 0 END) between 28200 and 32200)
				AND (sum(CASE WHEN c2 = 'it''s quoted' THEN 1 ELSE 0 END) between 22800 and 26800)
				AND (sum(CASE WHEN c2 IS NULL THEN 1 ELSE 0 END) between 22900 and 26900)
				AND (sum(CASE WHEN c3 IS NULL THEN 1 ELSE 0 END) = 0)
				from t1;`,
			engines: []string{"pg"},
			cmds:    [][]string{[]string{"--rows=100000", "--table=t1", "--stat-file=tests/pg/pg_stats.json"}},
		},
		// Every column of this key is a type the generator can write but the
		// sampler had no reader for, or had the wrong one: uuid and numeric
		// arrive as bytes, boolean as a bool, and a timestamp written back
		// with a Go-shaped layout is not the instant it was read. A key only
		// joins if all four round-trip exactly.
		{
			name:       "fk_key_types",
			checkQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id and t1.amount = t2.t1_amount and t1.flag = t2.t1_flag and t1.created_at = t2.t1_created_at;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=sequential"}},
		},

		// A coin flip of the default 1% over a 50-row parent is expected to
		// bring back half a row, and brings back none often enough to be the
		// normal outcome. The guard rail used to measure --rows, the table
		// being filled, so a small parent never tripped it.
		{
			name:       "fk_binomial_small_parent",
			checkQuery: "select count(*) = 5000 from t2 join t1 on t1.id = t2.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=50", "--table=t1"}, []string{"--rows=5000", "--table=t2", "--default-relationship=binomial", "--bulk-size=1000"}},
		},

		// More children than the parent has rows. A sequential relationship
		// is 1-1 while the parent lasts and a round robin past that, so every
		// parent row is used and none of the children is left unfilled.
		{
			name:       "fk_sequential_fanout",
			checkQuery: "select (count(*) = 250) and (count(distinct t2.t1_id) = 100) from t2 join t1 on t1.id = t2.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=250", "--table=t2", "--default-relationship=sequential"}},
		},

		// A column of a type nothing can generate is left out of the INSERT.
		// NOT NULL and with no default, that can only be rejected by the
		// engine, naming a column the user never mentioned, so it is refused
		// up front instead.
		{
			name:      "unsupported_type_not_null",
			engines:   []string{"pg"},
			cmds:      [][]string{[]string{"--rows=100", "--table=t1"}},
			expectErr: "no value can be generated for public.t1.source",
		},

		// Nullable, the same column is only worth a warning: the run works
		// and the column stays empty.
		{
			name:       "unsupported_type_nullable",
			checkQuery: "select (count(*) = 100) and (sum(CASE WHEN source IS NULL THEN 1 ELSE 0 END) = 100) and (sum(CASE WHEN note IS NOT NULL THEN 1 ELSE 0 END) = 100) from t1;",
			engines:    []string{"pg"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}},
		},

		// json documents are generated rather than left out: a document is
		// usually the widest thing a row holds, and a row missing it is not
		// the row being reproduced.
		{
			name:       "json",
			checkQuery: "select (count(*) = 100) and (sum(CASE WHEN doc->>'generated_by' = 'random-data-load' THEN 1 ELSE 0 END) = 100) from t1;",
			engines:    []string{"pg"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}},
		},
		{
			name:       "json",
			checkQuery: "select (count(*) = 100) and (sum(CASE WHEN json_unquote(json_extract(doc, '$.generated_by')) = 'random-data-load' THEN 1 ELSE 0 END) = 100) from t1;",
			engines:    []string{"mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}},
		},

		// A value pinned by hand that the query also filters on. The two used
		// to be registered separately and drawn independently, so the
		// selectivity asked for, 0.28, came out at 0.28 + 0.1*(1-0.28).
		{
			name:       "values_freq_map_query_overlap",
			checkQuery: "select (count(*) = 20000) AND (sum(CASE WHEN c2 = 'DHL' THEN 1 ELSE 0 END) between 5300 and 5900) from t1;",
			inputQuery: "select * from t1 where c2 = 'DHL'",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=20000", "--table=t1", "--null-freq=0", "--values-freq-map=t1.c2=DHL:0.28"}},
		},
	}

	for _, test := range tests {
		for _, engine := range test.engines {
			errlog := fmt.Sprintf("engine: %s, container: %s, testname: %s", engine, testsdb[engine].resource.Container.Name, test.name)
			if !keepDB() {
				errlog = fmt.Sprintf("to repeat the test and keep the container running, use KEEP_DB=1 go test .\n%s", errlog)
			}

			switch engine {
			case "mysql":
				errlog += fmt.Sprintf("\ndocker exec -it %s mysql -u dockertest -pdockertest test", testsdb[engine].resource.Container.Name)
			case "pg":
				errlog += fmt.Sprintf("\ndocker exec -it %s bash -c 'PGPASSWORD=dockertest psql -U dockertest test'", testsdb[engine].resource.Container.Name)
			}
			errlog += "\n"

			if err := ddl(engine, "reset"); err != nil {
				t.Fatalf("%sfailed to reset table schema: %v", errlog, err)
			}
			if err := ddl(engine, test.name); err != nil {
				t.Fatalf("%sfailed to apply test ddl: %v", errlog, err)
			}

			// calling tool with args directly
			for _, cmd := range test.cmds {
				args := []string{"run", "--engine=" + engine, "--host=127.0.0.1", "--user=dockertest", "--password=dockertest", "--database=test", "--port=" + testsdb[engine].port}
				args = append(args, cmd...)

				if test.inputQuery != "" {
					args = append(args, "--query="+test.inputQuery)
				}
				errlog += toolExecutable + " " + strings.Join(args, " ") + "\n"

				out, err := exec.Command(toolExecutable, args...).CombinedOutput()
				if test.expectErr != "" {
					// The run has to refuse the job rather than insert
					// nothing and report success: a script checking $? has
					// no other way to know.
					if err == nil {
						t.Fatalf("%sexpected %s to fail with %q, it succeeded. out: %s", errlog, toolExecutable, test.expectErr, out)
					}
					if !strings.Contains(string(out), test.expectErr) {
						t.Fatalf("%sexpected the failure to mention %q, out: %s", errlog, test.expectErr, out)
					}
					continue
				}
				if err != nil {
					t.Fatalf("%sfailed to exec %s: %v, out: %s", errlog, toolExecutable, err, out)
				}
			}

			if test.checkQuery == "" {
				continue
			}

			row := testsdb[engine].db.QueryRow(test.checkQuery)
			var ok bool
			err := row.Scan(&ok)
			if err != nil {
				t.Fatalf("%sfailed to query check sql: %v", errlog, err)
			}
			if !ok {
				t.Fatalf("%ssql check returned false, query:\n%s", errlog, test.checkQuery)
			}
		}
	}
}

func ddl(engine, name string) error {
	ddl, err := os.ReadFile(fmt.Sprintf("tests/%s/%s", engine, name))
	if err != nil {
		return fmt.Errorf("failed to read %s testcase %s: %v", engine, name, err)
	}

	// loading table schema
	_, err = testsdb[engine].db.Exec(string(ddl))
	if err != nil {
		return fmt.Errorf("failed to exec %s ddl for testname %s: %v", engine, name, err)
	}
	return nil
}

func keepDB() bool {
	return os.Getenv("KEEP_DB") == "1"
}
