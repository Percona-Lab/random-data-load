package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Percona-Lab/random-data-load/query"
	"github.com/pkg/errors"
)

// ExportStatCmd prints a command, it does not run one. The statistics worth
// copying live on the database the query is slow on, which is rarely the one
// being filled: this way the command can be handed over, reviewed, and run by
// whoever has access to that database.
type ExportStatCmd struct {
	Engine string `enum:"mysql,pg" required:"" help:"mysql,pg"`

	Query  string `help:"Applicative query. Its tables and columns are what the dump gets narrowed down to."`
	Table  string `help:"Table to gather statistics for. With --query, it restricts the dump to that single table."`
	Schema string `help:"Schema holding the tables." default:"public"`

	MaxCommonVals int    `name:"max-common-vals" help:"Keep only the first N most common values per column. postgres stores up to --default-statistics-target of them, which can be a lot of text. 0 keeps them all." default:"0"`
	Output        string `help:"File the printed command writes the dump to." default:"pg_stats.json"`
	SQLOnly       bool   `name:"sql-only" help:"Print the bare SQL instead of a psql invocation."`

	Host     string `help:"Host to put in the printed psql invocation. Omitted when empty."`
	Port     int    `help:"Port to put in the printed psql invocation. Omitted when 0."`
	User     string `help:"User to put in the printed psql invocation. Omitted when empty."`
	Database string `help:"Database to put in the printed psql invocation. Omitted when empty."`
}

func (cmd *ExportStatCmd) Run() error {
	if cmd.Query == "" && cmd.Table == "" {
		return errors.New("Need either a --query or a --table")
	}
	if err := cmd.supported(); err != nil {
		return err
	}

	tables, columns, err := cmd.scope()
	if err != nil {
		return err
	}
	if len(tables) == 0 {
		return errors.New("no table found to gather statistics for")
	}

	sql := cmd.sql(tables, columns)
	if cmd.SQLOnly {
		fmt.Println(sql)
		return nil
	}

	fmt.Print(cmd.shellCommand(sql))
	return nil
}

// scope narrows the dump down to what the run will actually generate.
//
// The column list is the same whitelist --query already builds to decide which
// fields to generate, so a dump and a run started from one query agree on what
// is needed. It comes back empty when the query selects a "*", and the dump
// then covers every column of the tables instead.
// supported reports what this command can export for the given engine.
//
// Only postgres keeps what the frequency options need. mysql has no equivalent
// of pg_stats: information_schema.COLUMN_STATISTICS holds histograms rather
// than a list of common values, and only for the columns someone built one for
// with ANALYZE TABLE ... UPDATE HISTOGRAM ON.
func (cmd *ExportStatCmd) supported() error {
	switch cmd.Engine {
	case "pg":
		return nil
	case "mysql":
		return errors.New("--engine=mysql cannot be exported yet: mysql only keeps histograms, in information_schema.COLUMN_STATISTICS, and only for the columns an explicit \"ANALYZE TABLE ... UPDATE HISTOGRAM ON\" built one for. Set the frequencies by hand with --null-freq-map and --values-freq-map")
	}
	return errors.Errorf("unimplemented engine %q", cmd.Engine)
}

func (cmd *ExportStatCmd) scope() ([]string, []string, error) {
	if cmd.Query == "" {
		return []string{cmd.Table}, nil, nil
	}

	// parsed in the same dialect the run will use, so that a query accepted
	// here is accepted there
	parsedTables, identifiers, _, _, err := query.ParseQuery(cmd.Query, cmd.Engine, true)
	if err != nil {
		return nil, nil, err
	}

	tables := sortedKeys(parsedTables)
	if cmd.Table != "" {
		tables = []string{cmd.Table}
	}
	return tables, sortedKeys(identifiers), nil
}

// sql builds the query dumping the statistics as a single JSON document.
//
// JSON rather than CSV because most_common_vals holds production values:
// commas, quotes and newlines are all fair game, and a JSON array carries them
// without a quoting convention to agree on.
func (cmd *ExportStatCmd) sql(tables, columns []string) string {
	slice := ""
	if cmd.MaxCommonVals > 0 {
		slice = fmt.Sprintf("[1:%d]", cmd.MaxCommonVals)
	}

	var b strings.Builder
	b.WriteString("SELECT coalesce(json_agg(s), '[]'::json)\n")
	b.WriteString("  FROM (SELECT schemaname, tablename, attname, null_frac,\n")
	// most_common_vals is an anyarray: it has no output function of its own,
	// so it goes through text before it can be read as an array of values.
	fmt.Fprintf(&b, "               (most_common_vals::text::text[])%s AS most_common_vals,\n", slice)
	fmt.Fprintf(&b, "               most_common_freqs%s\n", slice)
	b.WriteString("          FROM pg_stats\n")
	fmt.Fprintf(&b, "         WHERE schemaname = %s\n", quote(cmd.Schema))
	fmt.Fprintf(&b, "           AND lower(tablename) IN (%s)", quoteLowered(tables))
	if len(columns) > 0 {
		fmt.Fprintf(&b, "\n           AND lower(attname) IN (%s)", quoteLowered(columns))
	}
	b.WriteString(") s;")
	return b.String()
}

func (cmd *ExportStatCmd) shellCommand(sql string) string {
	psql := []string{"psql", "-X", "-q", "-A", "-t"}
	if cmd.Host != "" {
		psql = append(psql, "-h "+cmd.Host)
	}
	if cmd.Port != 0 {
		psql = append(psql, fmt.Sprintf("-p %d", cmd.Port))
	}
	if cmd.User != "" {
		psql = append(psql, "-U "+cmd.User)
	}
	if cmd.Database != "" {
		psql = append(psql, "-d "+cmd.Database)
	}

	var b strings.Builder
	b.WriteString("# Reads pg_stats and writes nothing. Run it on the database whose data\n")
	b.WriteString("# distribution you want to reproduce, then pass the file to:\n")
	fmt.Fprintf(&b, "#   %s run --stat-file=%s ...\n", toolname, cmd.Output)
	fmt.Fprintf(&b, "%s -f - > %s <<'SQL'\n", strings.Join(psql, " "), cmd.Output)
	b.WriteString(sql)
	b.WriteString("\nSQL\n")
	return b.String()
}

const toolname = "random-data-load"

// quote renders a string literal. These names come from a parsed query rather
// than from anything hostile, but they land in SQL either way.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// quoteLowered compares on lower() rather than on the name as written: an
// unquoted identifier reached pg_stats folded, and a name the query spelled in
// mixed case would never match it.
func quoteLowered(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, quote(strings.ToLower(strings.Trim(name, "\"`"))))
	}
	return strings.Join(quoted, ", ")
}

func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
