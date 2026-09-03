package cmd

import (
	"strings"
	"testing"
)

func TestExportStatSQL(t *testing.T) {
	tests := []struct {
		name    string
		cmd     ExportStatCmd
		want    []string // substrings the generated SQL must contain
		notWant []string
	}{
		{
			name: "a query narrows the dump down to its own tables and columns",
			cmd:  ExportStatCmd{Engine: "pg", Query: "select o.total from customers c join orders o on c.id = o.customer_id", Schema: "public"},
			want: []string{
				"lower(tablename) IN ('customers', 'orders')",
				"lower(attname) IN (",
				"'customer_id'", "'id'", "'total'",
			},
		},
		{
			name: "a query selecting everything cannot narrow the columns down",
			cmd:  ExportStatCmd{Engine: "pg", Query: "select * from orders", Schema: "public"},
			want: []string{"lower(tablename) IN ('orders')"},
			// the whitelist comes back empty on a "*", and filtering on an
			// empty list would dump nothing at all
			notWant: []string{"attname) IN"},
		},
		{
			name:    "--table alone dumps every column of that table",
			cmd:     ExportStatCmd{Engine: "pg", Table: "orders", Schema: "sales"},
			want:    []string{"schemaname = 'sales'", "lower(tablename) IN ('orders')"},
			notWant: []string{"attname) IN"},
		},
		{
			name: "--table restricts the tables a query found, keeping its columns",
			cmd:  ExportStatCmd{Engine: "pg", Query: "select o.total from customers c join orders o on c.id = o.customer_id", Table: "orders", Schema: "public"},
			want: []string{"lower(tablename) IN ('orders')", "'total'"},
		},
		{
			// the parser folds a bare identifier on its own, but a quoted one
			// keeps the case it was written in, and pg_stats holds it folded
			// unless the column really was created quoted
			name: "a quoted name is folded to match the catalog",
			cmd:  ExportStatCmd{Engine: "pg", Query: `select "MixedCol" from "MixedTable"`, Schema: "public"},
			want: []string{"lower(tablename) IN ('mixedtable')", "'mixedcol'"},
		},
		{
			name: "--table given with its quotes still matches",
			cmd:  ExportStatCmd{Engine: "pg", Table: `"MixedTable"`, Schema: "public"},
			want: []string{"lower(tablename) IN ('mixedtable')"},
		},
		{
			name: "--max-common-vals slices both arrays together",
			cmd:  ExportStatCmd{Engine: "pg", Table: "orders", Schema: "public", MaxCommonVals: 20},
			want: []string{"(most_common_vals::text::text[])[1:20]", "most_common_freqs[1:20]"},
		},
		{
			name:    "the arrays are left whole by default",
			cmd:     ExportStatCmd{Engine: "pg", Table: "orders", Schema: "public"},
			notWant: []string{"[1:"},
		},
		{
			name: "an empty result still parses as a dump",
			cmd:  ExportStatCmd{Engine: "pg", Table: "orders", Schema: "public"},
			want: []string{"coalesce(json_agg(s), '[]'::json)"},
		},
		{
			name: "a quote in a name is escaped rather than closing the literal",
			cmd:  ExportStatCmd{Engine: "pg", Table: "o'rders", Schema: "it's"},
			want: []string{"schemaname = 'it''s'", "lower(tablename) IN ('o''rders')"},
		},
		{
			name: "only the three figures the tool can reproduce are asked for",
			cmd:  ExportStatCmd{Engine: "pg", Table: "orders", Schema: "public"},
			want: []string{"null_frac", "most_common_vals", "most_common_freqs"},
			// pg_stats is wide, and the rest of it would be dead weight in a
			// file meant to travel out of a production database
			notWant: []string{"n_distinct", "histogram_bounds", "correlation", "avg_width", "*"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tables, columns, err := test.cmd.scope()
			if err != nil {
				t.Fatalf("scope(): %v", err)
			}
			got := test.cmd.sql(tables, columns)

			for _, want := range test.want {
				if !strings.Contains(got, want) {
					t.Errorf("generated SQL is missing %q:\n%s", want, got)
				}
			}
			for _, notWant := range test.notWant {
				if strings.Contains(got, notWant) {
					t.Errorf("generated SQL should not contain %q:\n%s", notWant, got)
				}
			}
		})
	}
}

func TestExportStatShell(t *testing.T) {
	cmd := ExportStatCmd{Engine: "pg", Table: "orders", Schema: "public", Output: "stats.json",
		Host: "db1", Port: 5433, User: "app", Database: "shop"}
	got := cmd.shellCommand(cmd.sql([]string{"orders"}, nil))

	for _, want := range []string{
		"psql -X -q -A -t -h db1 -p 5433 -U app -d shop -f - > stats.json <<'SQL'",
		"\nSQL\n",
		"--stat-file=stats.json",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated command is missing %q:\n%s", want, got)
		}
	}

	// The SQL travels in a quoted heredoc, so the shell must not touch it. That
	// only holds while nothing in the body can end the heredoc early.
	body := got[strings.Index(got, "<<'SQL'\n")+len("<<'SQL'\n"):]
	if strings.Count(body, "\nSQL\n") != 1 {
		t.Errorf("the heredoc body ends more than once:\n%s", body)
	}
}

func TestExportStatOmitsUnsetConnectionFlags(t *testing.T) {
	cmd := ExportStatCmd{Engine: "pg", Table: "orders", Schema: "public", Output: "pg_stats.json"}
	got := cmd.shellCommand("SELECT 1;")

	if !strings.Contains(got, "psql -X -q -A -t -f - > pg_stats.json") {
		t.Errorf("unset connection flags should be left out entirely:\n%s", got)
	}
	for _, notWant := range []string{"-h ", "-p ", "-U ", "-d "} {
		if strings.Contains(got, notWant) {
			t.Errorf("generated command should not contain %q:\n%s", notWant, got)
		}
	}
}

func TestExportStatNeedsATarget(t *testing.T) {
	cmd := ExportStatCmd{Engine: "pg", Schema: "public"}
	if err := cmd.Run(); err == nil {
		t.Fatal("Run() without a --query or a --table should fail")
	}
}

func TestExportStatEngines(t *testing.T) {
	tests := []struct {
		engine  string
		wantErr string // substring the error has to carry
	}{
		{engine: "pg"},
		// erroring out beats printing a command that cannot produce the
		// frequencies the run needs
		{engine: "mysql", wantErr: "COLUMN_STATISTICS"},
		{engine: "sqlite", wantErr: "unimplemented engine"},
		{engine: "", wantErr: "unimplemented engine"},
	}

	for _, test := range tests {
		t.Run(test.engine, func(t *testing.T) {
			cmd := ExportStatCmd{Engine: test.engine, Table: "orders", Schema: "public", Output: "stat.json"}
			err := cmd.Run()

			switch {
			case test.wantErr == "" && err != nil:
				t.Fatalf("Run() on --engine=%s: %v", test.engine, err)
			case test.wantErr == "":
			case err == nil:
				t.Fatalf("Run() on --engine=%s should have failed", test.engine)
			case !strings.Contains(err.Error(), test.wantErr):
				t.Errorf("Run() on --engine=%s failed with %q, want it to mention %q", test.engine, err, test.wantErr)
			}
		})
	}
}
