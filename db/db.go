package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
)

type Config struct {
	Engine   string `enum:"mysql,pg" required:"" help:"mysql,pg"`
	Database string
	Host     string
	User     string
	Password string
	Port     int
}

var (
	DB     *sql.DB
	engine Engine
)

type Engine interface {
	Connect(Config) (*sql.DB, error)
	GetFields(string, string) ([]Field, error)
	GetConstraints(string, string) ([]*Constraint, error)
	InsertTemplate() string
	Escape(string) string
	EscapeValue(string) string
	SetTableMetadata(*Table, string, string)
	BinomialWhereClause(float64) string
	ErrShouldRetryTx(error) bool
	FilterOnRowNumberFromClause([]Field, string, string) string
	FilterOnRowNumberVarClause() string
	ValueTimeLayout() string
	TruncateTables([]*Table) error
	Analyze(string, string) error
	TableStorage(string, string) (Storage, error)
	GetUniqueKeys(string, string) ([][]string, error)
}

var ErrFieldsNotFound = errors.New("fields not found")

func Connect(config Config) (*sql.DB, error) {
	err := setEngine(config)
	if err != nil {
		return nil, err
	}
	DB, err = engine.Connect(config)
	return DB, err
}

func setEngine(config Config) error {
	switch config.Engine {
	case "mysql":
		engine = MySQL{}
		return nil
	case "pg":
		engine = Postgres{}
		return nil
	default:
		return errors.New("unsupported engine")
	}
}

func GetFields(schema, table string) ([]Field, error) {
	return engine.GetFields(schema, table)
}

func GetConstraints(schema, table string) ([]*Constraint, error) {
	return engine.GetConstraints(schema, table)
}

// GetUniqueKeys returns every set of columns of a table that has to stay
// unique, primary key included.
func GetUniqueKeys(schema, table string) ([][]string, error) {
	return engine.GetUniqueKeys(schema, table)
}

// scanKeyColumns reads a query returning one semicolon-joined column list per
// key. Both catalogs are asked the same question and neither can return an
// array a driver would read, so both aggregate into a string.
func scanKeyColumns(query string, args ...interface{}) ([][]string, error) {
	rows, err := DB.Query(query, args...)
	if err != nil {
		return nil, errors.Wrapf(err, "reading the unique keys, query: %s, args: %v", query, args)
	}
	defer rows.Close()

	keys := [][]string{}
	for rows.Next() {
		var joined string
		if err := rows.Scan(&joined); err != nil {
			return nil, errors.Wrap(err, "reading the unique keys")
		}
		if joined == "" {
			continue
		}
		keys = append(keys, strings.Split(joined, ";"))
	}
	return keys, errors.Wrap(rows.Err(), "reading the unique keys")
}

func InsertTemplate() string {
	return engine.InsertTemplate()
}

// TruncateTables empties the tables a run is about to fill.
//
// Nothing here drops a row of a table the run was not going to write to: the
// foreign keys pointing in from outside the set are what makes that refuse
// rather than cascade, which is the answer worth having.
func TruncateTables(tables []*Table) error {
	if len(tables) == 0 {
		return nil
	}
	return engine.TruncateTables(tables)
}

func Escape(s string) string {
	return engine.Escape(s)
}

// EscapeValue makes a string safe to paste between the single quotes of a
// literal. Values coming from a query, a --values-freq-map or a pg_stats dump
// are arbitrary production data, so they routinely contain quotes.
func EscapeValue(s string) string {
	if engine == nil {
		return s
	}
	return engine.EscapeValue(s)
}

func BinomialWhereClause(freqPercent float64) string {
	return engine.BinomialWhereClause(freqPercent)
}

func ErrShouldRetryTx(err error) bool {
	return engine.ErrShouldRetryTx(err)
}

func FilterOnRowNumberFromClause(fields []Field, table, schema string) string {
	return engine.FilterOnRowNumberFromClause(fields, table, schema)
}

func FilterOnRowNumberVarClause() string {
	return engine.FilterOnRowNumberVarClause()
}

// ValueTimeLayout is how a date read from a parent row has to be written back
// for the engine to store the same instant. A sampled key only matches its
// parent if it round-trips exactly.
func ValueTimeLayout() string {
	if engine == nil {
		return time.RFC3339Nano
	}
	return engine.ValueTimeLayout()
}

// CountRows counts the rows of a table.
//
// The samplers need the size of the parent they read from: it decides how
// large a Bernoulli draw has to be to bring anything back, where a sequential
// pager has to wrap around, and what range of row numbers the normal and zipf
// laws may draw. An estimate from the catalog would be free but stale, and a
// sampler paging past the end of a table it has the wrong size for comes back
// empty, which is fatal.
func CountRows(schema, table string) (int64, error) {
	query := fmt.Sprintf("SELECT count(*) FROM %s.%s", Escape(schema), Escape(table))
	var count int64
	if err := DB.QueryRow(query).Scan(&count); err != nil {
		return 0, errors.Wrapf(err, "cannot count the rows of %s.%s", schema, table)
	}
	return count, nil
}

// RowNumberedSubquery wraps a table so its rows can be asked for by position.
//
// Both engines have window functions, so both get the same subquery. The
// alternative on mysql, a user variable incremented as the rows go by, cannot
// be selected and compared against in the same statement without being
// incremented twice per row.
//
// Rows holding a NULL in any of the columns are left out, so the numbering is
// dense over the rows that can actually fill a foreign key. Pair it with
// CountNonNullRows, which counts the same set.
func RowNumberedSubquery(fields []Field, schema, table string) string {
	columns := EscapedNamesListFromFields(fields)
	return fmt.Sprintf("(SELECT %s, ROW_NUMBER() OVER (ORDER BY %s) AS rownumber FROM %s.%s WHERE %s) f",
		columns, columns, Escape(schema), Escape(table), EscapedFieldsIsNotNull(fields))
}

// CountNonNullRows counts the rows of a table that can fill a foreign key,
// which is the rows holding no NULL in any of its columns.
func CountNonNullRows(schema, table string, fields []Field) (int64, error) {
	query := fmt.Sprintf("SELECT count(*) FROM %s.%s WHERE %s",
		Escape(schema), Escape(table), EscapedFieldsIsNotNull(fields))
	var count int64
	if err := DB.QueryRow(query).Scan(&count); err != nil {
		return 0, errors.Wrapf(err, "cannot count the usable rows of %s.%s", schema, table)
	}
	return count, nil
}

// HasAnyRow reports whether a table holds anything at all.
//
// The question a foreign key asks of a parent is not how many rows it has but
// whether it has one, and a count over a table that is already loaded is a
// full scan for an answer the first row settles.
func HasAnyRow(schema, table string) (bool, error) {
	query := fmt.Sprintf("SELECT 1 FROM %s.%s LIMIT 1", Escape(schema), Escape(table))
	var found int
	err := DB.QueryRow(query).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, errors.Wrapf(err, "cannot tell whether %s.%s holds any row", schema, table)
}
