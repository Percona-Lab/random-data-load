package db

import (
	"database/sql"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/pkg/errors"
)

// Table holds the table definition with all fields, indexes and triggers
type Table struct {
	Schema string
	Name   string
	Fields []Field
	//Indexes     map[string]Index
	Constraints     []Constraint
	ColToConstraint map[string]*Constraint
}

/*
type Index struct {
	Name       string
	Fields     []string
	IsUnique   bool
	IsNullable bool
}
*/
// Constraint holds Foreign Keys information
type Constraint struct {
	ConstraintName        string
	ReferencedTableSchema string
	ReferencedTableName   string
	ColumnsName           []string // sorted by ordinal_position
	ReferencedColumsName  []string
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
}

func NewTable() *Table {
	var table Table
	table.ColToConstraint = map[string]*Constraint{}
	return &table
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
	table := NewTable()
	engine.SetTableMetadata(table, database, tablename)

	table.Fields, err = GetFields(table.Schema, table.Name)
	if err != nil {
		return nil, errors.Wrap(err, "LoadTable")
	}

	table.Constraints, err = GetConstraints(table.Schema, table.Name)
	if err != nil {
		return nil, errors.Wrap(err, "LoadTable")
	}
	table.resolveConstraints()

	log.Debug().Strs("fields", table.FieldNames()).Int("lenConstraints", len(table.Constraints)).Msg("loaded table")
	return table, nil
}

func (t *Table) resolveConstraints() {
	for _, constraint := range t.Constraints {
		for _, col := range constraint.ColumnsName {
			t.ColToConstraint[col] = &constraint
		}
	}
}

func (t *Table) FieldByName(name string) *Field {
	for _, field := range t.Fields {
		if field.ColumnName == name {
			return &field
		}
	}
	return nil
}

func (t *Table) FieldsToGenerate() []Field {
	fields := []Field{}

	for _, field := range t.Fields {
		if !isSupportedType(field.DataType) {
			continue
		}
		if !field.IsNullable && field.ColumnKey == "PRI" && field.AutoIncrement {
			continue
		}
		if _, ok := t.ColToConstraint[field.ColumnName]; ok {
			continue
		}
		fields = append(fields, field)
	}
	return fields
}

func (t *Table) FieldsToSample() []Field {
	fields := []Field{}

	for _, field := range t.Fields {
		if _, ok := t.ColToConstraint[field.ColumnName]; !ok {
			continue
		}
		if !isSupportedType(field.DataType) {
			log.Error().Str("field", field.ColumnName).Str("type", field.DataType).Msg("Unsupported type is part of a foreign key relationship and must be skipped. It may break foreign key links")
			continue
		}
		fields = append(fields, field)
	}
	return fields
}

func isSupportedType(fieldType string) bool {
	supportedTypes := map[string]bool{
		"tinyint":    true,
		"smallint":   true,
		"mediumint":  true,
		"int":        true,
		"integer":    true,
		"bigint":     true,
		"float":      true,
		"decimal":    true,
		"double":     true,
		"char":       true,
		"varchar":    true,
		"date":       true,
		"datetime":   true,
		"timestamp":  true,
		"time":       true,
		"year":       true,
		"tinyblob":   true,
		"tinytext":   true,
		"blob":       true,
		"text":       true,
		"mediumblob": true,
		"mediumtext": true,
		"longblob":   true,
		"longtext":   true,
		"binary":     true,
		"varbinary":  true,
		"enum":       true,
		"set":        true,
		"bool":       true,
		"boolean":    true,
	}
	_, ok := supportedTypes[fieldType]
	return ok
}

func EscapedNamesListFromFields(fields []Field) string {
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, Escape(field.ColumnName))
	}
	return strings.Join(names, ",")
}
