package db

import (
	"strings"

	"slices"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/ylacancellera/random-data-load/query"
)

// Constraint holds Foreign Keys information
type Constraint struct {
	ConstraintName              string
	ReferencedTableSchema       string
	ReferencedTableName         string
	ColumnsName                 []string // sorted by ordinal_position
	ReferencedColumnsName       []string
	Fields                      []Field
	ReferencedFields            []Field
	ReferencedTable             *Table
	willBeInsertedDuringThisRun bool
}

type Constraints []*Constraint

func NewConstraintFromVirtualFK(table *Table, left query.VirtualJoinPart, right query.VirtualJoinPart) (*Constraint, error) {

	constraint := &Constraint{
		ConstraintName:        "VirtualFK_" + strings.Join(right.Columns, "_") + gofakeit.ID(), // an ID to prevent collisions
		ReferencedTableSchema: table.Schema,                                                    // assuming the schema is the same, good enough for now
		ReferencedTableName:   left.Table,
		ColumnsName:           right.Columns,
		ReferencedColumnsName: left.Columns,
	}
	constraint.populateFields(table)
	err := constraint.loadReferencedTable()
	return constraint, errors.Wrap(err, "NewConstraintFromVirtualFK")
}

func (c *Constraint) IsLooping() bool {
	return c.constraintLoopTraverser([]string{})
}

func (c *Constraint) constraintLoopTraverser(traversedTables []string) bool {
	if slices.Contains(traversedTables, c.ReferencedTable.Name) {
		return true
	}
	for _, childConstraints := range c.ReferencedTable.Constraints {
		isLooping := childConstraints.constraintLoopTraverser(append(traversedTables, c.ReferencedTable.Name))
		if isLooping {
			return true
		}
	}
	return false
}

func (cs Constraints) Fields() []Field {
	fields := []Field{}
	for _, c := range cs {
		fields = append(fields, c.Fields...)
	}
	return fields
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
	for _, colname := range c.ReferencedColumnsName {

		refField := c.ReferencedTable.FieldByName(colname)
		if refField == nil {
			return errors.Errorf("could not find column %s from table %s", colname, c.ReferencedTable.Name)
		}
		c.ReferencedFields = append(c.ReferencedFields, *refField)
	}
	return nil
}

func shouldSkipVirtualFK(tables []*Table, vfk query.VirtualJoin) bool {

	// source and target is in the order of the written query, not necessarily in the logical order
	// source would be the parent table
	// target would be the child, which could have had an actual FOREIGN KEY object
	// so the current t *Table should be the target: it points to a dependency

	for _, table := range tables {
		for _, constraint := range table.Constraints {
			log.Debug().
				Interface("left", vfk.Left).Interface("right", vfk.Right).Str("loopCurrentTable", table.Name).
				Str("loopReferencedTable", constraint.ReferencedTableName).Strs("loopReferencedColumnsName", constraint.ReferencedColumnsName).Strs("loopConstraintColumnsName", constraint.ColumnsName).
				Msg("filtering virtual keys")

			switch {
			// TODO: we could "supplement" existing FKs with virtual ones, I'm not sure if that's a real use case yet
			case strings.ToLower(vfk.Left.Table) == strings.ToLower(table.Name) &&
				strings.ToLower(vfk.Right.Table) == strings.ToLower(constraint.ReferencedTableName) &&
				isSliceSimilar(constraint.ColumnsName, vfk.Left.Columns) &&
				isSliceSimilar(constraint.ReferencedColumnsName, vfk.Right.Columns):
				return true

				// flipped
			case strings.ToLower(vfk.Right.Table) == strings.ToLower(table.Name) &&
				strings.ToLower(vfk.Left.Table) == strings.ToLower(constraint.ReferencedTableName) &&
				isSliceSimilar(constraint.ColumnsName, vfk.Right.Columns) &&
				isSliceSimilar(constraint.ReferencedColumnsName, vfk.Left.Columns):

				return true
			}

		}
	}
	return false
}

func AddVirtualFKs(tables []*Table, fkeys []query.VirtualJoin) error {
	log.Debug().Interface("fkeys", fkeys).Str("func", "AddVirtualFKs2").Msg("adding virtual foreign keys")

	for _, virtualJoin := range fkeys {

		if shouldSkipVirtualFK(tables, virtualJoin) {
			log.Debug().Str("left", virtualJoin.Left.Table).Str("right", virtualJoin.Right.Table).Str("func", "AddVirtualFKs").Msg("already handled by schema's constraint, skipping")
			continue
		}

		// left is parent, right is child. Constraints are on child side
		tableIdx := slices.IndexFunc(tables, func(t *Table) bool { return strings.ToLower(t.Name) == strings.ToLower(virtualJoin.Right.Table) })
		if tableIdx == -1 {
			log.Debug().Str("left", virtualJoin.Left.Table).Str("right", virtualJoin.Right.Table).Str("func", "AddVirtualFKs").Msg("table not loaded")
			continue
		}
		table := tables[tableIdx]

		constraint, err := NewConstraintFromVirtualFK(table, virtualJoin.Left, virtualJoin.Right)
		if err != nil {
			log.Error().Str("left", virtualJoin.Left.Table).Str("right", virtualJoin.Right.Table).Str("func", "AddVirtualFKs").Err(err).Msg("could not add a virtual foreign key, skipping")
			return errors.Wrap(err, "AddVirtualFKs")
		}

		if constraint.IsLooping() {
			constraint, err = NewConstraintFromVirtualFK(table, virtualJoin.Right, virtualJoin.Left)
			if err != nil {
				log.Error().Str("left", virtualJoin.Right.Table).Str("right", virtualJoin.Left.Table).Str("func", "AddVirtualFKs").Err(err).Msg("could not add a (flipped) virtual foreign key, skipping")
				return errors.Wrap(err, "AddVirtualFKs")
			}
			if constraint.IsLooping() {
				log.Debug().Str("left", virtualJoin.Left.Table).Str("right", virtualJoin.Right.Table).Str("func", "AddVirtualFKs").Msg("could not add a virtual foreign key without creating a loop, skipping")
			}
		}

		table.Constraints = append(table.Constraints, constraint)

		log.Debug().Str("left", virtualJoin.Left.Table).Str("right", virtualJoin.Right.Table).Str("func", "AddVirtualFKs").Msg("virtual foreign key added")
	}

	return nil
}
