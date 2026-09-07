package db

import (
	"database/sql"
	"fmt"
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

func InsertTemplate() string {
	return engine.InsertTemplate()
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
