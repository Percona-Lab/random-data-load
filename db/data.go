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
	Fields                []Field
	ReferencedFields      []Field
	ReferencedTable       *Table
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

func newTable() *Table {
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
	table := newTable()
	engine.SetTableMetadata(table, database, tablename)

	table.Fields, err = GetFields(table.Schema, table.Name)
	if err != nil {
		return nil, errors.Wrap(err, "LoadTable")
	}

	table.Constraints, err = GetConstraints(table.Schema, table.Name)
	if err != nil {
		return nil, errors.Wrap(err, "LoadTable")
	}
	//TODO currently not protected against cyclical dependencies
	err = table.resolveConstraints()
	if err != nil {
		return nil, errors.Wrap(err, "resolveConstraints")
	}

	log.Debug().Strs("fields", table.FieldNames()).Int("lenConstraints", len(table.Constraints)).Str("tablename", table.Name).Str("table schema", table.Schema).Msg("loaded table")
	return table, nil
}

func (t *Table) resolveConstraints() error {
	var err error
	for constraintIdx, constraint := range t.Constraints {

		t.Constraints[constraintIdx].ReferencedTable, err = LoadTable(constraint.ReferencedTableSchema, constraint.ReferencedTableName)
		if err != nil {
			return errors.Wrap(err, "resolveConstraints recursive loadtable")
		}

		for colnameIdx, colname := range constraint.ColumnsName {

			t.ColToConstraint[colname] = &constraint

			field := t.FieldByName(colname)
			if field == nil {
				return errors.Errorf("could not find column %s from table %s", colname, t.Name)
			}
			t.Constraints[constraintIdx].Fields = append(t.Constraints[constraintIdx].Fields, *field)
			refField := t.Constraints[constraintIdx].ReferencedTable.FieldByName(t.Constraints[constraintIdx].ReferencedColumsName[colnameIdx])
			if refField == nil {
				return errors.Errorf("could not find column %s from table %s", t.Constraints[constraintIdx].ReferencedColumsName[colnameIdx], t.Constraints[constraintIdx].ReferencedTable.Name)
			}
			t.Constraints[constraintIdx].ReferencedFields = append(t.Constraints[constraintIdx].ReferencedFields, *refField)
		}
	}
	return nil
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

// FieldsToSample points to the fields of the table we are looking to insert to
func (t *Table) FieldsToSample() []Field {
	fields := []Field{}

	for _, constraint := range t.Constraints {
	ITERATE_FK_COLUMNS:
		for _, colname := range constraint.ColumnsName {
			for _, field := range t.Fields {
				if field.ColumnName == colname {
					fields = append(fields, field)
					continue ITERATE_FK_COLUMNS
				}
			}
		}
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
