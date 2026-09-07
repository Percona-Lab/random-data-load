package db

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/lib/pq"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

var postgresTypeMapping = map[string]string{
	"numeric":                     "decimal",
	"double precision":            "double",
	"character varying":           "varchar",
	"time with time zone":         "time",
	"timestamp with time zone":    "timestamp",
	"timestamp without time zone": "timestamp",
}

type Postgres struct{}

func (_ Postgres) Connect(dbInfo Config) (*sql.DB, error) {
	if dbInfo.Port == 0 {
		dbInfo.Port = 5432
	}
	return sql.Open("postgres", fmt.Sprintf("user=%s dbname=%s password=%s sslmode=disable host=%s port=%d ", dbInfo.User, dbInfo.Database, dbInfo.Password, dbInfo.Host, dbInfo.Port))
}

func (postgres Postgres) GetFields(schema, tablename string) ([]Field, error) {
	query := `SELECT
		column_name, 
		is_nullable::boolean, 
		data_type, 
		coalesce(character_maximum_length, 2000),
		numeric_precision, 
		numeric_scale, 
		CASE WHEN is_identity='YES' THEN 'PRI' else '' END,
		CASE WHEN identity_generation='ALWAYS' THEN true else false END,
		column_default is not null,
		CASE WHEN is_generated='ALWAYS' THEN true ELSE false END
	FROM information_schema.columns
	WHERE table_schema=$1 AND table_name=$2`

	rows, err := DB.Query(query, schema, tablename)
	if err != nil {
		return []Field{}, errors.Wrapf(err, "postgres.GetFields: query: %s, schema: %s, table: %s", query, schema, tablename)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return []Field{}, errors.Wrap(err, "Cannot get column names")
	}

	var found bool
	fields := []Field{}
	for rows.Next() {
		found = true
		var f Field

		var columnType string
		scanRecipients := postgres.makeScanRecipients(&f, &columnType, cols)
		err := rows.Scan(scanRecipients...)
		if err != nil {
			log.Error().Err(err).Msg("cannot get fields")
			continue
		}

		if replacment, ok := postgresTypeMapping[f.DataType]; ok {
			f.DataType = replacment
		}
		fields = append(fields, f)
	}
	if err = rows.Err(); err != nil {
		return []Field{}, err
	}
	if !found {
		return []Field{}, errors.Wrapf(ErrFieldsNotFound, "query: %s", query)
	}
	return fields, nil
}

func (_ Postgres) makeScanRecipients(f *Field, columnType *string, cols []string) []interface{} {
	fields := []interface{}{
		&f.ColumnName,
		&f.IsNullable,
		&f.DataType,
		&f.CharacterMaximumLength,
		//&f.CharacterOctetLength,
		&f.NumericPrecision,
		&f.NumericScale,
		//&columnType,
		&f.ColumnKey,
		&f.AutoIncrement,
		&f.HasDefaultValue,
		&f.IsGenerated,
	}

	return fields
}

func (_ Postgres) GetConstraints(schema, tablename string) ([]*Constraint, error) {
	// information_schema cannot tell two foreign keys sharing a name apart:
	// their name only has to be unique within their own table, so every schema
	// holding a copy of that table, and even a sibling table in this one,
	// reports the same name. Joined on it, their columns pile up into a single
	// constraint listing each column once per namesake. pg_constraint holds
	// the columns of one key, paired up by position, and nothing else.
	query := `
SELECT con.conname,
	refnamespace.nspname as referenced_schema_name,
	refclass.relname as referenced_table_name,
	string_agg(refattribute.attname, ';' ORDER BY cols.ord) as referenced_column_names,
	string_agg(attribute.attname, ';' ORDER BY cols.ord) as column_names
FROM pg_catalog.pg_constraint con
JOIN pg_catalog.pg_class class
	ON class.oid = con.conrelid
JOIN pg_catalog.pg_namespace namespace
	ON namespace.oid = class.relnamespace
JOIN pg_catalog.pg_class refclass
	ON refclass.oid = con.confrelid
JOIN pg_catalog.pg_namespace refnamespace
	ON refnamespace.oid = refclass.relnamespace
CROSS JOIN LATERAL unnest(con.conkey, con.confkey) WITH ORDINALITY AS cols(attnum, refattnum, ord)
JOIN pg_catalog.pg_attribute attribute
	ON attribute.attrelid = con.conrelid
	AND attribute.attnum = cols.attnum
JOIN pg_catalog.pg_attribute refattribute
	ON refattribute.attrelid = con.confrelid
	AND refattribute.attnum = cols.refattnum
WHERE con.contype = 'f'
	AND namespace.nspname = $1
	AND class.relname = $2
GROUP BY con.oid, con.conname, refnamespace.nspname, refclass.relname
ORDER BY con.conname;
		`
	rows, err := DB.Query(query, schema, tablename)
	if err != nil {
		return nil, errors.Wrapf(err, "get constraints, query: %s, schema: %s, table: %s", query, schema, tablename)
	}
	defer rows.Close()

	constraints := []*Constraint{}

	for rows.Next() {
		var c Constraint
		var columnsNameAgg, refColumnsNameAgg string
		err := rows.Scan(&c.ConstraintName, &c.ReferencedTableSchema,
			&c.ReferencedTableName, &refColumnsNameAgg, &columnsNameAgg)
		if err != nil {
			return nil, fmt.Errorf("cannot read constraints: %s", err)
		}
		c.ColumnsName = strings.Split(columnsNameAgg, ";")
		c.ReferencedColumnsName = strings.Split(refColumnsNameAgg, ";")
		constraints = append(constraints, &c)

	}

	return constraints, nil
}
func (_ Postgres) InsertTemplate() string {
	return "INSERT INTO %s.%s (%s) VALUES \n"
}

func (_ Postgres) Escape(s string) string {
	return "\"" + s + "\""
}

// EscapeValue doubles single quotes. With standard_conforming_strings on, the
// default since 9.1, a backslash carries no special meaning and must be left
// alone.
func (_ Postgres) EscapeValue(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func (_ Postgres) SetTableMetadata(table *Table, database, tablename string) {
	// database is useless for catalogs, it's only used for connection on pg
	schema := "public"
	if elems := strings.Split(tablename, "."); len(elems) > 1 {
		schema = elems[0]
		tablename = elems[1]
	}
	table.Schema = schema
	table.Name = tablename
}

func (_ Postgres) BinomialWhereClause(freqPercent float64) string {
	return "TABLESAMPLE BERNOULLI (" + fmt.Sprintf("%.10f", freqPercent) + ") WHERE 1=1"
}

func (_ Postgres) ErrShouldRetryTx(err error) bool {
	return strings.Contains(err.Error(), "duplicate key value violates unique constraint")
}

func (_ Postgres) FilterOnRowNumberFromClause(fields []Field, table, schema string) string {
	escapedFields := EscapedNamesListFromFields(fields)
	return fmt.Sprintf("(SELECT %s, ROW_NUMBER() OVER (ORDER BY %s) as rownumber FROM %s.%s ) f", escapedFields, escapedFields, Escape(schema), Escape(table))
}

func (_ Postgres) FilterOnRowNumberVarClause() string {
	return "rownumber"
}

// ValueTimeLayout keeps the offset the value was read with, so that a
// timestamptz sampled from a parent row is stored as the same instant whatever
// the session's TimeZone is.
func (_ Postgres) ValueTimeLayout() string {
	return "2006-01-02 15:04:05.999999-07:00"
}
