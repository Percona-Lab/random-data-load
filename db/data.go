package db

import (
	"database/sql"

	"github.com/pkg/errors"
)

// Table holds the table definition with all fields, indexes and triggers
type Table struct {
	Schema      string
	Name        string
	Fields      []Field
	Indexes     map[string]Index
	Constraints []Constraint
}

// Index holds the basic index information
type Index struct {
	Name       string
	Fields     []string
	IsUnique   bool
	IsNullable bool
}

// Constraint holds Foreign Keys information
type Constraint struct {
	ConstraintName        string
	ColumnName            string
	ReferencedTableSchema string
	ReferencedTableName   string
	ReferencedColumnName  string
}

// Field holds raw field information as defined in INFORMATION_SCHEMA
type Field struct {
	ColumnName             string
	IsNullable             bool
	DataType               string
	CharacterMaximumLength sql.NullInt64
	NumericPrecision       sql.NullInt64
	NumericScale           sql.NullInt64
	AutoIncrement          bool
	ColumnKey              string
	SetEnumVals            []string
	Constraint             *Constraint
}

// FieldNames returns an string array with the table's field names
func (t *Table) FieldNames() []string {
	fields := []string{}
	for _, field := range t.Fields {
		fields = append(fields, field.ColumnName)
	}
	return fields
}

func LoadTable(database, tablename string) (*Table, error) {
	var err error
	table := engine.SetTableMetadata(database, tablename)

	table.Fields, err = GetFields(table.Schema, table.Name)
	if err != nil {
		return nil, errors.Wrap(err, "LoadTable")
	}

	table.Constraints, err = GetConstraints(table.Schema, table.Name)
	if err != nil {
		return nil, errors.Wrap(err, "LoadTable")
	}
	return &table, nil
}
