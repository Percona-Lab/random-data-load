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

	var mysqldb *sql.DB
	if err = pool.Retry(func() error {
		mysqldb, err = sql.Open("mysql", fmt.Sprintf("dockertest:dockertest@(localhost:%s)/test?multiStatements=true", mysqlresource.GetPort("3306/tcp")))
		if err != nil {
			return err
		}
		return mysqldb.Ping()
	}); err != nil {
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
			name:       "fk_uniform",
			checkQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=1-1"}},
		},

		// not a great test for now, but we want some matches, but not every lines matched
		{
			name:       "fk_db_random",
			checkQuery: "select count(*) between 1 and 99 from t1 join t2 on t1.id = t2.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=db-random-1-n"}},
		},

		{
			name:       "fk_multicol",
			checkQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id and t1.id2 = t2.t1_id2;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=1-1"}},
		},

		{
			name:       "basic_query",
			checkQuery: "select (count(*) = 100) and (sum(CASE WHEN c2 IS NULL THEN 1 ELSE 0 END) = 100)  from t1 where c1 is not null;",
			inputQuery: "select c1 from t1;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}},
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

		/*
			{
				name:       "identifiers_skip_fk_multicol",
				checkQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id and t1.id2 = t2.t1_id2;",
				inputQuery: "select a1.id, a1.id2 from t1 a1 join t2 a2 on a1.id = a2.t1_id;",
				engines:    []string{"pg", "mysql"},
				cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=1-1"}},
			},
		*/

		{
			name:       "fk_virtual",
			checkQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id;",
			inputQuery: "select * from t1 join t2 on t1.id = t2.t1_id;",
			engines:    []string{"pg", "mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=1-1"}},
		},

		/* not working yet. Will have to wait for proper recursive table load instead of 1 table per execution
		{
			name:       "fk_virtual_cascade",
			checkQuery: "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id join t3 on t2.id = t3.t2_id join t4 on t3.id = t4.t3_id;",
			inputQuery: "select * from t1 join t2 on t1.id = t2.t1_id join t3 on t2.id = t3.t2_id join t4 on t3.id = t4.t3_id;",
			engines:    []string{"mysql"},
			cmds:       [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=1-1"}, []string{"--rows=100", "--table=t3", "--default-relationship=1-1"}, []string{"--rows=100", "--table=t4", "--default-relationship=1-1"}},
		},
		*/
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
				if err != nil {
					t.Fatalf("%sfailed to exec %s: %v, out: %s", errlog, toolExecutable, err, out)
				}
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
