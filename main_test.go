package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
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
	defer func() {
		for _, resource := range []*dockertest.Resource{pgresource, mysqlresource} {
			if err := pool.Purge(resource); err != nil {
				log.Panicf("Could not purge resource: %s", err)
			}
		}
	}()

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
	m.Run()
}

func TestRun(t *testing.T) {

	tests := []struct {
		name    string
		query   string // used to check if the generated result seems appropriate
		engines []string
		tables  []string
		cmds    [][]string
	}{
		{
			name:    "basic",
			query:   "select count(*) = 10 from t1;",
			engines: []string{"pg", "mysql"},
			cmds:    [][]string{[]string{"--rows=10", "--table=t1"}},
		},

		{
			name:    "pk_bigserial",
			query:   "select count(*) = 100 from t1;",
			engines: []string{"pg"},
			cmds:    [][]string{[]string{"--rows=100", "--table=t1"}},
		},
		{
			name:    "pk_identity",
			query:   "select count(*) = 100 from t1 where id < 101;",
			engines: []string{"pg"},
			cmds:    [][]string{[]string{"--rows=100", "--table=t1"}},
		},
		{
			name:    "pk",
			query:   "select count(*) = 100 from t1;",
			engines: []string{"pg", "mysql"},
			cmds:    [][]string{[]string{"--rows=100", "--table=t1"}},
		},
		{
			name:    "pk_auto_increment",
			query:   "select count(*) = 100 from t1 where id < 101;",
			engines: []string{"mysql"},
			cmds:    [][]string{[]string{"--rows=100", "--table=t1"}},
		},

		{
			name:    "pk_varchar",
			query:   "select count(*) = 100 from t1;",
			engines: []string{"pg", "mysql"},
			cmds:    [][]string{[]string{"--rows=100", "--table=t1"}},
		},

		{
			name:    "bool",
			query:   "select (count(*) = 100) and (sum(CASE WHEN c1 THEN 1 ELSE 0 END) between 1 and 99) from t1;",
			engines: []string{"pg", "mysql"},
			cmds:    [][]string{[]string{"--rows=100", "--table=t1"}},
		},

		{
			name:    "fk_uniform",
			query:   "select count(*) = 100 from t1 join t2 on t1.id = t2.t1_id;",
			engines: []string{"pg", "mysql"},
			cmds:    [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=1-1"}},
		},

		// not a great test for now, but we want some matches, but not every lines matched
		{
			name:    "fk_db_random",
			query:   "select count(*) between 1 and 99 from t1 join t2 on t1.id = t2.t1_id;",
			engines: []string{"pg", "mysql"},
			cmds:    [][]string{[]string{"--rows=100", "--table=t1"}, []string{"--rows=100", "--table=t2", "--default-relationship=db-random-1-n"}},
		},
	}

	for _, test := range tests {
		for _, engine := range test.engines {
			if err := ddl(engine, "reset"); err != nil {
				t.Error(err)
				continue
			}
			if err := ddl(engine, test.name); err != nil {
				t.Error(err)
				continue
			}

			// calling tool with args directly
			for _, cmd := range test.cmds {
				args := []string{"run", "--engine=" + engine, "--host=127.0.0.1", "--user=dockertest", "--password=dockertest", "--database=test", "--port=" + testsdb[engine].port}
				args = append(args, cmd...)

				out, err := exec.Command(toolExecutable, args...).CombinedOutput()
				if err != nil {
					t.Errorf("failed to exec %s for testname %s %s: %v, out: %s", toolExecutable, engine, test.name, err, out)
					continue
				}
			}

			row := testsdb[engine].db.QueryRow(test.query)
			var ok bool
			err := row.Scan(&ok)
			if err != nil {
				t.Errorf("failed to query check sql for testname %s %s: %v", engine, test.name, err)
			}
			if !ok {
				t.Errorf("sql check returned false for testname %s %s", engine, test.name)
				abortAndKeepContainersRunning(engine, test.query)
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

func abortAndKeepContainersRunning(engine, testquery string) {
	if os.Getenv("KEEP_DB") == "1" {
		log.Fatalf("Keep databases running after error\nYou can connect to %s container %s to check manually\n%s", engine, testsdb[engine].resource.Container.Name, testquery)
	}
}
