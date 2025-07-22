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
	HasDefaultValue        bool
	skip                   bool
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
	for constraintIdx := range table.Constraints {
		table.Constraints[constraintIdx].populateFields(table)
		err = table.Constraints[constraintIdx].loadReferencedTable()
		if err != nil {
			return nil, errors.Wrap(err, "LoadTable")
		}
	}
	table.resolveColToConstraints()

	log.Debug().Strs("fields", table.FieldNames()).Int("lenConstraints", len(table.Constraints)).Str("tablename", table.Name).Str("table schema", table.Schema).Msg("loaded table")
	return table, nil
}

func (t *Table) resolveColToConstraints() {
	for _, constraint := range t.Constraints {
		for _, colname := range constraint.ColumnsName {
			t.ColToConstraint[colname] = &constraint
		}
	}
}

func (c *Constraint) populateFields(targetTable *Table) error {

	for _, colname := range c.ColumnsName {

		field := targetTable.FieldByName(colname)
		if field == nil {
			return errors.Errorf("could not find column %s from table %s", colname, targetTable.Name)
		}
		c.Fields = append(c.Fields, *field)
	}
	return nil
}

func (c *Constraint) loadReferencedTable() error {

	var err error
	c.ReferencedTable, err = LoadTable(c.ReferencedTableSchema, c.ReferencedTableName)
	if err != nil {
		return errors.Wrap(err, "loadReferencedTable")
	}
	for _, colname := range c.ReferencedColumsName {

		refField := c.ReferencedTable.FieldByName(colname)
		if refField == nil {
			return errors.Errorf("could not find column %s from table %s", colname, c.ReferencedTable.Name)
		}
		c.ReferencedFields = append(c.ReferencedFields, *refField)
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
		if field.skip {
			continue
		}
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
		for _, field := range t.Fields {
			if slices.Contains(constraint.ColumnsName, field.ColumnName) && !field.skip {
				fields = append(fields, field)
				continue ITERATE_FK_COLUMNS
			}
		}
	}
	return fields
}

func (t *Table) SkipBasedOnIdentifiers(identifiers map[string]struct{}) {
	log.Debug().Interface("identifiers", identifiers).Str("tablename", t.Name).Str("table schema", t.Schema).Str("func", "skipBasedOnIdentifiers").Msg("init")
	for i, field := range t.Fields {
		if _, ok := identifiers[field.ColumnName]; !ok && field.skippeable() {
			log.Debug().Str("field", field.ColumnName).Str("tablename", t.Name).Str("table schema", t.Schema).Str("func", "skipBasedOnIdentifiers").Msg("set skip")
			field.skip = true
			t.Fields[i] = field
			continue
		}
		log.Debug().Str("field", field.ColumnName).Str("tablename", t.Name).Str("table schema", t.Schema).Bool("skippeable", field.skippeable()).Str("func", "skipBasedOnIdentifiers").Msg("don't skip")
	}
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

func FilterVirtualFKs(tables []*Table, fkeys map[string]string) {
	// source and target is in the order of the written query, not necessarily in the logical order
	// source would be the parent table
	// target would be the child, which could have had an actual FOREIGN KEY object
	// so the current t *Table should be the target: it points to a dependency
	for source, target := range fkeys {
		sourceTable, sourceCol, ok1 := strings.Cut(source, ".")
		targetTable, targetCol, ok2 := strings.Cut(target, ".")
		if !ok1 || !ok2 {
			log.Debug().Str("key", source).Str("value", target).Str("func", "FilterVirtualFKs").Msg("malformed virtualfk")
			delete(fkeys, source)
			continue
		}

		for _, table := range tables {
			for _, constraint := range table.Constraints {
				switch {
				case sourceTable == table.Name && targetTable == constraint.ReferencedTableName &&
					slices.Contains(constraint.ColumnsName, sourceCol) && slices.Contains(constraint.ReferencedColumsName, targetCol):
					delete(fkeys, source)

					// flipped
				case targetTable == table.Name && sourceTable == constraint.ReferencedTableName &&
					slices.Contains(constraint.ColumnsName, targetCol) && slices.Contains(constraint.ReferencedColumsName, sourceCol):
					delete(fkeys, source)
				}

			}
		}
	}
}

func AddVirtualFKs(tables []*Table, fkeys map[string]string) error {
	for source, target := range fkeys {
		sourceTable, sourceCol, _ := strings.Cut(source, ".")
		targetTable, targetCol, _ := strings.Cut(target, ".")

		var table *Table
		tableIdx := slices.IndexFunc(tables, func(t *Table) bool { return t.Name == sourceTable })
		targetTableIdx := slices.IndexFunc(tables, func(t *Table) bool { return t.Name == targetTable })
		switch {
		case tableIdx != -1 && targetTableIdx != -1:
			// sets virtual FK on tables with the most fk already
			// a table without foreign keys will ensure the tool can run with no additional actions
			if len(tables[targetTableIdx].Constraints) > len(tables[tableIdx].Constraints) {
				table = tables[targetTableIdx]
			} else {
				table = tables[tableIdx]
			}
		case tableIdx != -1:
			table = tables[tableIdx]
		case targetTableIdx != -1:
			table = tables[targetTableIdx]
		default:
			log.Warn().Str("key", source).Str("value", target).Str("func", "AddVirtualFKs").Msg("none of those tables are loaded")
			continue

		}

		constraint := Constraint{
			ConstraintName:        "VirtualFK_" + targetCol,
			ReferencedTableSchema: table.Schema, // assuming the schema is the same, good enough for now
			ReferencedTableName:   sourceTable,
			ColumnsName:           []string{targetCol},
			ReferencedColumsName:  []string{sourceCol},
		}
		constraint.populateFields(table)
		err := constraint.loadReferencedTable()
		if err != nil {
			return errors.Wrap(err, "AddVirtualFKs")
		}
		table.Constraints = append(table.Constraints, constraint)
		delete(fkeys, source)

		// already called once on the real constraints, but it's safe to call twice to resolve the virtual FKs
		table.resolveColToConstraints()
	}

	return nil
}

func (f *Field) skippeable() bool {
	log.Debug().Str("field", f.ColumnName).Bool("nullable", f.IsNullable).Bool("hasDefault", f.HasDefaultValue).Str("func", "skippeable").Msg("debug skippeable")
	if !f.IsNullable && !f.HasDefaultValue {
		return false
	}
	return true
}
