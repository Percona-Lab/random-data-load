package db

import (
	"database/sql"
	"strings"

	"slices"

	"github.com/rs/zerolog/log"

	"github.com/pkg/errors"
)

// Table holds the table definition with all fields, indexes and triggers
type Table struct {
	Schema string
	Name   string
	Fields []Field
	//Indexes     map[string]Index
	Constraints []*Constraint
}

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
	HasDefaultValue        bool
	Skip                   bool
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
		"uuid":       true,
		"bool":       true,
		"boolean":    true,
	}
	_, ok := supportedTypes[fieldType]
	return ok
}

var loadedTableCache = map[string]*Table{}

func LoadTable(database, tablename string) (*Table, error) {
	if table, ok := loadedTableCache[database+"."+tablename]; ok {
		return table, nil
	}

	var err error
	table := &Table{}
	engine.SetTableMetadata(table, database, tablename)

	table.Fields, err = GetFields(table.Schema, table.Name)
	if err != nil {
		return nil, errors.Wrapf(err, "LoadTable %s.%s", database, tablename)
	}

	table.Constraints, err = GetConstraints(table.Schema, table.Name)
	if err != nil {
		return nil, errors.Wrapf(err, "LoadTable %s.%s", database, tablename)
	}

	loadedTableCache[table.FullName()] = table

	for constraintIdx := range table.Constraints {
		table.Constraints[constraintIdx].populateFields(table)
		err = table.Constraints[constraintIdx].loadReferencedTable()
		if err != nil {
			return nil, errors.Wrapf(err, "LoadTable %s.%s", database, tablename)
		}
	}

	log.Debug().Strs("fields", table.FieldNames()).Int("lenConstraints", len(table.Constraints)).Str("tablename", table.Name).Str("table schema", table.Schema).Msg("loaded table")
	return table, nil
}

// FieldNames returns an string array with the table's field names
func (t *Table) FieldNames() []string {
	fields := []string{}
	for _, field := range t.Fields {
		fields = append(fields, field.ColumnName)
	}
	return fields
}

func (t *Table) FieldByName(name string) *Field {
	for _, field := range t.Fields {
		if strings.ToLower(field.ColumnName) == strings.ToLower(name) {
			return &field
		}
	}
	return nil
}

func (t *Table) FullName() string {
	return t.Schema + "." + t.Name
}

// only needed for pg, but mysql does not mind
// you can't "insert into sometable () values ()" in pg, it will require at least
// insert into sometable (id) values (default)
func (t *Table) FieldsToInsertAsDefault() []Field {
	fields := []Field{}

	// let's skip this when possible
	if len(t.FieldsToGenerate())+len(t.ConstraintsToSample()) != 0 {
		return fields
	}

	for _, field := range t.Fields {
		if !field.IsNullable && field.ColumnKey == "PRI" && field.AutoIncrement {
			fields = append(fields, field)
		}
	}
	return fields
}

func (t *Table) FieldsToGenerate() []Field {
	fields := []Field{}

	for _, field := range t.Fields {
		if field.Skip {
			continue
		}
		if !isSupportedType(field.DataType) {
			continue
		}
		if !field.IsNullable && field.ColumnKey == "PRI" && field.AutoIncrement {
			continue
		}
		if t.IsFieldInAnyConstraints(field) {
			continue
		}

		fields = append(fields, field)
	}
	return fields
}

func (t *Table) IsFieldInAnyConstraints(field Field) bool {
	for _, constraint := range t.Constraints {
		if slices.ContainsFunc(constraint.ColumnsName, func(s string) bool { return strings.ToLower(s) == strings.ToLower(field.ColumnName) }) {
			return true
		}
	}
	return false
}

// currently not checking for any field required for 2 constraints at the same time
// should not happen since it does not make sense for FKs
func (t *Table) ConstraintsToSample() Constraints {
	cs := []*Constraint{}
CONSTRAINTS:
	for _, constraint := range t.Constraints {
		for _, field := range constraint.Fields {
			// if only 1 field is needed, all fields from this constraint will be needed too
			if !field.Skip {
				cs = append(cs, constraint)
				continue CONSTRAINTS
			}
		}
	}
	return cs
}

func (t *Table) FlagConstraintThatArePartsOfThisRun(tables []*Table) {
	for _, constraint := range t.Constraints {
		if slices.ContainsFunc(tables, func(t2 *Table) bool {
			return strings.ToLower(t2.Name) == strings.ToLower(constraint.ReferencedTableName)
		}) {
			constraint.willBeInsertedDuringThisRun = true
		}
		log.Debug().Bool("willBeInsertedDuringThisRun", constraint.willBeInsertedDuringThisRun).Str("constraint", constraint.ConstraintName).Str("tablename", t.Name).Str("table schema", t.Schema).Str("func", "FlagConstraintThatArePartsOfThisRun").Msg("will constraint be resolved during this run")
	}
}

func (t *Table) SkipBasedOnIdentifiers(identifiers map[string]struct{}) {
	log.Debug().Interface("identifiers", identifiers).Str("tablename", t.Name).Str("table schema", t.Schema).Str("func", "skipBasedOnIdentifiers").Msg("init")
	if len(identifiers) == 0 {
		return
	}
	for i, field := range t.Fields {
		_, ok := identifiers[field.ColumnName]
		log.Debug().Str("field", field.ColumnName).Str("tablename", t.Name).Str("table schema", t.Schema).Str("func", "skipBasedOnIdentifiers").Bool("fieldSkippeable", field.skippeable()).Bool("foundInIdentifiers", ok).Bool("will be skipped", !ok && field.skippeable()).Msg("will field be skipped")
		if !ok && field.skippeable() {
			field.Skip = true
			t.Fields[i] = field
			continue
		}
	}
}

// are all dependencies/referenced tables present in this list of tables
func (t *Table) AreAllDependenciesContained(tables []*Table) bool {
	for _, constraint := range t.Constraints {
		// if some tables won't be part of this run, we should not wait for this dependencies to be "loaded" already in the table running order
		if !constraint.willBeInsertedDuringThisRun {
			continue
		}
		if !slices.ContainsFunc(tables, func(t2 *Table) bool {
			return strings.ToLower(t2.Name) == strings.ToLower(constraint.ReferencedTableName)
		}) {
			return false
		}
	}
	return true
}

func (t *Table) HasAnyConstraintLoop() bool {
	for _, c := range t.Constraints {
		if c.IsLooping() {
			return true
		}
	}
	return false
}

func (t *Table) IdentifyAndResolveSelfReferencingConstraintLoop() (*Table, error) {
	for cidx, c := range t.Constraints {
		if c.ReferencedTable.Name == t.Name {
			for _, field := range c.Fields {
				if !field.IsNullable {
					return nil, errors.Errorf("table %s is self referencing and one of fields (%s) is non-nullable. Consider dropping the foreign key named %s", t.Name, field.ColumnName, c.ConstraintName)
				}
			}
			copiedTable := *t

			copiedTable.Fields = slices.Clone(t.Fields)
			for fidx, field := range copiedTable.Fields {
				if !slices.ContainsFunc(c.ColumnsName, func(s string) bool { return strings.ToLower(s) == strings.ToLower(field.ColumnName) }) {
					continue
				}
				field.Skip = true
				copiedTable.Fields[fidx] = field
			}

			copiedTable.Constraints = slices.Clone(t.Constraints)
			copiedTable.Constraints = append(copiedTable.Constraints[:cidx], copiedTable.Constraints[cidx+1:]...)
			c.ReferencedTable = &copiedTable
			log.Debug().Str("table", t.Name).Interface("base table constraints", t.Constraints).Interface("copied table constraints", copiedTable.Constraints).Int("constraintLen", len(t.Constraints)).Int("copiedConstraintLen", len(copiedTable.Constraints)).Interface("base table fields", t.Fields).Interface("copied table fields", copiedTable.Fields).Msg("table has a self-referencing foreign key. Cloning table")
			return &copiedTable, nil
		}
	}
	return nil, nil
}

func (f *Field) skippeable() bool {
	if !f.IsNullable && !f.HasDefaultValue {
		return false
	}
	return true
}
