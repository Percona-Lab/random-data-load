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
	return sql.Open("postgres", fmt.Sprintf("user=%s dbname=%s password=%s sslmode=disable host=%s port=%d ", dbInfo.User, dbInfo.Database, dbInfo.Password, dbInfo.Host, dbInfo.Port))
}

func (postgres Postgres) GetFields(schema, tablename string) ([]Field, error) {
	query := `SELECT
		column_name, 
		is_nullable::boolean, 
		data_type, 
		character_maximum_length, 
		numeric_precision, 
		numeric_scale, 
		CASE WHEN is_identity='YES' THEN 'PRI' else '' END,
		CASE WHEN identity_generation='ALWAYS' THEN true else false END
	FROM information_schema.columns
	WHERE table_schema=$1 AND table_name=$2`

	rows, err := DB.Query(query, schema, tablename)
	if err != nil {
		return []Field{}, errors.Wrap(err, "postgres.GetFields")
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
		return []Field{}, errors.New("fields not found")
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
	}

	return fields
}

func (_ Postgres) GetConstraints(schema, tablename string) ([]Constraint, error) {
	query := `
SELECT c.constraint_name, 
	y.table_schema as referenced_schema_name, 
	y.table_name as refereneced_table_name, 
	string_agg(y.column_name, ';' ORDER BY x.ordinal_position) as referenced_column_names, 
	string_agg(x.column_name, ';' ORDER BY x.ordinal_position) as column_names
FROM information_schema.referential_constraints c
JOIN information_schema.key_column_usage x
	ON x.constraint_name = c.constraint_name
JOIN information_schema.key_column_usage y
    ON y.ordinal_position = x.position_in_unique_constraint
    AND y.constraint_name = c.unique_constraint_name
WHERE x.table_schema = $1 
	AND x.table_name = $2
GROUP BY 1,2,3
ORDER BY c.constraint_name;
		`
	rows, err := DB.Query(query, schema, tablename)
	if err != nil {
		return nil, errors.Wrapf(err, "get constraints, query: %s, schema: %s, table: %s", query, schema, tablename)
	}
	defer rows.Close()

	constraints := []Constraint{}

	for rows.Next() {
		var c Constraint
		var columnsNameAgg, refColumnsNameAgg string
		err := rows.Scan(&c.ConstraintName, &c.ReferencedTableSchema,
			&c.ReferencedTableName, &refColumnsNameAgg, &columnsNameAgg)
		if err != nil {
			return nil, fmt.Errorf("cannot read constraints: %s", err)
		}
		c.ColumnsName = strings.Split(columnsNameAgg, ";")
		c.ReferencedColumsName = strings.Split(refColumnsNameAgg, ";")
		constraints = append(constraints, c)

	}

	return constraints, nil
}
func (_ Postgres) InsertTemplate() string {
	return "INSERT INTO %s.%s (%s) VALUES \n"
}

func (_ Postgres) Escape(s string) string {
	return "\"" + s + "\""
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

func (_ Postgres) DBRandomWhereClause() string {
	return "TABLESAMPLE BERNOULLI (10)"
}
