package db

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/lib/pq"
	"github.com/pkg/errors"
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
	return sql.Open("postgres", fmt.Sprintf("user=%s dbname=%s password=%s sslmode=disable host=%s port=%d ", dbInfo.User, dbInfo.Database, dbInfo.Password, dbInfo.Host, dbInfo.Port))
}

/*
SELECT
		pg_attribute.attname,
		pg_attribute.attnotnull,
		pg_type.typname,
		pg_catalog.format_type(pg_attribute.atttypid, pg_attribute.atttypmod),

	FROM pg_class
	JOIN pg_attribute ON pg_class.oid = pg_attribute.attrelid
	JOIN pg_type ON pg_type.oid = pg_attribute.atttypid
	JOIN pg_namespace ON pg_class.relnamespace=pg_namespace.oid
	WHERE pg_namespace.nspname='$1' AND relname='$2' AND attnum > 0
	ORDER BY attnum;

*/
func (_ Postgres) GetFields(schema, tablename string) ([]Field, error) {
	query := `SELECT
		column_name, 
		is_nullable::boolean, 
		data_type, 
		character_maximum_length, 
		numeric_precision, 
		numeric_scale 
	FROM information_schema.columns
	WHERE table_schema=$1 AND table_name=$2`

	rows, err := DB.Query(query, schema, tablename)
	if err != nil {
		return []Field{}, errors.Wrap(err, "postgres.GetFields")
	}
	defer rows.Close()

	fields := []Field{}
	for rows.Next() {
		var f Field
		toScan := []interface{}{
			&f.ColumnName,
			&f.IsNullable,
			&f.DataType,
			&f.CharacterMaximumLength,
			&f.NumericPrecision,
			&f.NumericScale,
		}
		err = rows.Scan(toScan...)
		if err != nil {
			// TODO: just mimicked mysql counterpart
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
	return fields, nil
}

func (_ Postgres) GetConstraints(schema, name string) ([]Constraint, error) {
	return []Constraint{}, nil

}

func (_ Postgres) InsertTemplate() string {
	return "INSERT INTO %s.%s (%s) VALUES \n"
}

func (_ Postgres) Escape(s string) string {
	return "\"" + s + "\""
}

func (_ Postgres) SetTableMetadata(database, tablename string) Table {
	// database is useless for catalogs, it's only used for connection on pg
	schema := "public"
	if elems := strings.Split(tablename, "."); len(elems) > 1 {
		schema = elems[0]
		tablename = elems[1]
	}
	table := Table{Schema: schema, Name: tablename}
	return table
}
