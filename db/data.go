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

// Constraint holds Foreign Keys information
type Constraint struct {
	ConstraintName              string
	ReferencedTableSchema       string
	ReferencedTableName         string
	ColumnsName                 []string // sorted by ordinal_position
	ReferencedColumsName        []string
	Fields                      []Field
	ReferencedFields            []Field
	ReferencedTable             *Table
	willBeInsertedDuringThisRun bool
}

type Constraints []*Constraint

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
	table := &Table{}
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

	log.Debug().Strs("fields", table.FieldNames()).Int("lenConstraints", len(table.Constraints)).Str("tablename", table.Name).Str("table schema", table.Schema).Msg("loaded table")
	return table, nil
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
		return errors.Wrapf(err, "using schema %s, table %s", c.ReferencedTableSchema, c.ReferencedTableName)
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
		if strings.ToLower(field.ColumnName) == strings.ToLower(name) {
			return &field
		}
	}
	return nil
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
		if field.skip {
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
			if !field.skip {
				cs = append(cs, constraint)
				continue CONSTRAINTS
			}
		}
	}
	return cs
}

func (cs Constraints) Fields() []Field {
	fields := []Field{}
	for _, c := range cs {
		fields = append(fields, c.Fields...)
	}
	return fields
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
			field.skip = true
			t.Fields[i] = field
			continue
		}
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
		"uuid":       true,
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
			log.Warn().Str("key", source).Str("value", target).Str("func", "FilterVirtualFKs").Msg("malformed virtual foreign key. Both key and value should look like {table}.{col}")
			delete(fkeys, source)
			continue
		}

		for _, table := range tables {
			for _, constraint := range table.Constraints {
				log.Debug().Str("sourceTable", sourceTable).Str("sourceCol", sourceCol).Str("targetTable", targetTable).Str("targetCol", targetCol).Str("loopCurrentTable", table.Name).Str("loopReferencedTable", constraint.ReferencedTableName).Strs("loopReferencedColumnsName", constraint.ReferencedColumsName).Strs("loopConstraintColumnsName", constraint.ColumnsName).Msg("filtering virtual keys")
				switch {
				case strings.ToLower(sourceTable) == strings.ToLower(table.Name) &&
					strings.ToLower(targetTable) == strings.ToLower(constraint.ReferencedTableName) &&
					slices.ContainsFunc(constraint.ColumnsName, func(s string) bool { return strings.ToLower(s) == strings.ToLower(sourceCol) }) &&
					slices.ContainsFunc(constraint.ReferencedColumsName, func(s string) bool { return strings.ToLower(s) == strings.ToLower(targetCol) }):

					delete(fkeys, source)

					// flipped
				case strings.ToLower(targetTable) == strings.ToLower(table.Name) &&
					strings.ToLower(sourceTable) == strings.ToLower(constraint.ReferencedTableName) &&
					slices.ContainsFunc(constraint.ColumnsName, func(s string) bool { return strings.ToLower(s) == strings.ToLower(targetCol) }) &&
					slices.ContainsFunc(constraint.ReferencedColumsName, func(s string) bool { return strings.ToLower(s) == strings.ToLower(sourceCol) }):

					delete(fkeys, source)
				}

			}
		}
	}
}

func AddVirtualFKs(tables []*Table, fkeys map[string]string) error {
	log.Debug().Interface("fkeys", fkeys).Str("func", "AddVirtualFKs").Msg("adding virtual foreign keys")
	for source, target := range fkeys {
		sourceTable, sourceCol, _ := strings.Cut(source, ".")
		targetTable, targetCol, _ := strings.Cut(target, ".")

		var table *Table
		// source is parent, target is child. Constraints are on child side
		tableIdx := slices.IndexFunc(tables, func(t *Table) bool { return strings.ToLower(t.Name) == strings.ToLower(targetTable) })
		if tableIdx == -1 {
			log.Debug().Str("key", source).Str("value", target).Str("func", "AddVirtualFKs").Msg("table not loaded")
			continue
		}
		table = tables[tableIdx]

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
			log.Error().Str("key", source).Str("value", target).Str("func", "AddVirtualFKs").Err(err).Msg("could not add a virtual foreign key, skipping")
			return errors.Wrap(err, "AddVirtualFKs")
		}
		table.Constraints = append(table.Constraints, &constraint)
		delete(fkeys, source)

		log.Debug().Str("key", source).Str("value", target).Str("func", "AddVirtualFKs").Msg("virtual foreign key added")
	}

	return nil
}

// sort the tables so that dependencies are inserted first
func SortTables(tables []*Table) []*Table {

	slices.SortFunc(tables, func(a, b *Table) int {
		return len(a.Constraints) - len(b.Constraints)
	})
	tablesSorted := make([]*Table, 0, cap(tables))
	tablesIndexes := make([]int, len(tables), cap(tables))

	// we get a slice for indexes of the main "tables" slices
	// we want to keep the "tables" untouched and reorganize it, tablesIndexes will track what is left to handle
	for i := 0; i < len(tables); i++ {
		tablesIndexes[i] = i
	}

INSERT_LOOP:
	for len(tablesIndexes) > 0 {
		for metaIndex, idx := range tablesIndexes {
			if tables[idx].AreAllDependenciesContained(tablesSorted) {
				log.Debug().Str("table", tables[idx].Name).Msg("all dep are contained, adding to running order")
				tablesSorted = append(tablesSorted, tables[idx])
				tablesIndexes = slices.Delete(tablesIndexes, metaIndex, metaIndex+1)
				continue INSERT_LOOP
			}
			log.Debug().Str("table", tables[idx].Name).Msg("not all deps are contained, continue")
		}
	}
	return tablesSorted
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

func (f *Field) skippeable() bool {
	if !f.IsNullable && !f.HasDefaultValue {
		return false
	}
	return true
}
