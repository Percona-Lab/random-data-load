package db

import (
	"strings"

	"slices"

	"github.com/Percona-Lab/random-data-load/query"
	"github.com/brianvoe/gofakeit/v7"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// Constraint holds Foreign Keys information
type Constraint struct {
	ConstraintName              string
	TableName                   string // the table holding the key, the child side
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
		TableName:             table.Name,
		ReferencedTableSchema: table.Schema, // assuming the schema is the same, good enough for now
		ReferencedTableName:   left.Table,
		ColumnsName:           right.Columns,
		ReferencedColumnsName: left.Columns,
	}
	constraint.populateFields(table)
	err := constraint.loadReferencedTable()
	return constraint, errors.Wrap(err, "NewConstraintFromVirtualFK")
}

// IsLooping reports whether the dependencies this constraint waits on lead
// back to the table holding it, leaving no order in which the tables can be
// inserted.
//
// The path starts on that table: a key is part of the loop it closes, and a
// traversal starting empty only ever sees the loops lying beyond it.
func (c *Constraint) IsLooping() bool {
	return c.constraintLoopTraverser([]string{c.TableName})
}

func (c *Constraint) constraintLoopTraverser(traversedTables []string) bool {
	// A table referencing itself is a loop this run knows how to break, by
	// inserting to it twice, so it does not make an order impossible. Left in,
	// it would also report every key merely pointing at such a table as
	// looping.
	if c.IsSelfReferencing() {
		return false
	}
	if slices.ContainsFunc(traversedTables, func(t string) bool { return strings.EqualFold(t, c.ReferencedTable.Name) }) {
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

// IsSelfReferencing reports whether the key points back at its own table.
func (c *Constraint) IsSelfReferencing() bool {
	return strings.EqualFold(c.TableName, c.ReferencedTableName)
}

// ColumnsName returns the columns every one of these constraints holds.
func (cs Constraints) ColumnsName() []string {
	columns := []string{}
	for _, c := range cs {
		columns = append(columns, c.ColumnsName...)
	}
	return columns
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

			// TODO: we could "supplement" existing FKs with virtual ones, I'm not sure if that's a real use case yet
			//
			// The query's own orientation carries no meaning, so both readings
			// of the guess are checked against the constraint.
			if constraint.covers(table.Name, vfk.Left, vfk.Right) ||
				constraint.covers(table.Name, vfk.Right, vfk.Left) {
				return true
			}
		}
	}
	return false
}

// covers reports whether this constraint already requires everything a virtual
// foreign key would add, reading child as the table holding the key.
//
// A guess taken from a query may name only part of a composite key: a query
// joining on one column of a two-column key states nothing the schema does not
// already, and adding a second constraint for that column would list it twice
// in the INSERT.
func (c *Constraint) covers(childTable string, child, parent query.VirtualJoinPart) bool {
	if !strings.EqualFold(childTable, child.Table) ||
		!strings.EqualFold(c.ReferencedTableName, parent.Table) ||
		len(child.Columns) != len(parent.Columns) ||
		len(child.Columns) == 0 {
		return false
	}

	for i, childColumn := range child.Columns {
		if !c.pairs(childColumn, parent.Columns[i]) {
			return false
		}
	}
	return true
}

// pairs reports whether the constraint links these two columns to each other.
// Their position within the key does not matter, only that they face one
// another.
func (c *Constraint) pairs(childColumn, parentColumn string) bool {
	for i, column := range c.ColumnsName {
		if !strings.EqualFold(column, childColumn) {
			continue
		}
		return i < len(c.ReferencedColumnsName) &&
			strings.EqualFold(c.ReferencedColumnsName[i], parentColumn)
	}
	return false
}

// isKeySide reports whether these columns are a primary or unique key of their
// table, the side a foreign key has to point at. It reports nothing for a
// table this run did not load, and for a catalog that does not say which
// columns are keys.
func isKeySide(tables []*Table, part query.VirtualJoinPart) bool {
	if len(part.Columns) == 0 {
		return false
	}
	tableIdx := slices.IndexFunc(tables, func(t *Table) bool { return strings.EqualFold(t.Name, part.Table) })
	if tableIdx == -1 {
		return false
	}

	for _, column := range part.Columns {
		field := tables[tableIdx].FieldByName(column)
		if field == nil || (field.ColumnKey != "PRI" && field.ColumnKey != "UNI") {
			return false
		}
	}
	return true
}

// virtualFKConstraint builds the constraint one reading of a guessed join asks
// for, parent being the table pointed at and child the one holding the key. It
// returns no table when the child is not part of this run.
func virtualFKConstraint(tables []*Table, parent, child query.VirtualJoinPart) (*Table, *Constraint, error) {
	tableIdx := slices.IndexFunc(tables, func(t *Table) bool { return strings.EqualFold(t.Name, child.Table) })
	if tableIdx == -1 {
		return nil, nil, nil
	}
	table := tables[tableIdx]

	constraint, err := NewConstraintFromVirtualFK(table, parent, child)
	if err != nil {
		return table, nil, err
	}
	return table, constraint, nil
}

func AddVirtualFKs(tables []*Table, fkeys []query.VirtualJoin) error {
	log.Debug().Interface("fkeys", fkeys).Str("func", "AddVirtualFKs2").Msg("adding virtual foreign keys")

	for _, virtualJoin := range fkeys {

		if shouldSkipVirtualFK(tables, virtualJoin) {
			log.Debug().Str("left", virtualJoin.Left.Table).Str("right", virtualJoin.Right.Table).Str("func", "AddVirtualFKs").Msg("already handled by schema's constraint, skipping")
			continue
		}

		// left is parent, right is child. Constraints are on child side
		parentPart, childPart := virtualJoin.Left, virtualJoin.Right

		// A foreign key can only point at a key. When one side of the join is
		// one and the other is not, that settles which table is the parent,
		// whatever order the query wrote them in: read the other way round,
		// the key would have the parent's own identity sampled from the
		// child's rows, and repeat it.
		if isKeySide(tables, childPart) && !isKeySide(tables, parentPart) {
			log.Debug().Str("parent", childPart.Table).Str("child", parentPart.Table).Str("func", "AddVirtualFKs").Msg("reading the join the other way round, the parent is the side holding the key")
			parentPart, childPart = childPart, parentPart
		}

		table, constraint, err := virtualFKConstraint(tables, parentPart, childPart)
		if err != nil {
			log.Error().Str("left", virtualJoin.Left.Table).Str("right", virtualJoin.Right.Table).Str("func", "AddVirtualFKs").Err(err).Msg("could not add a virtual foreign key, skipping")
			return errors.Wrap(err, "AddVirtualFKs")
		}
		if table == nil {
			log.Debug().Str("left", virtualJoin.Left.Table).Str("right", virtualJoin.Right.Table).Str("func", "AddVirtualFKs").Msg("table not loaded")
			continue
		}

		// The order the query wrote the equality in carries no meaning, so the
		// other reading is tried when this one would leave the tables with no
		// insert order. It belongs to the other table: the key sits on the
		// child, the side pointing at a dependency.
		if constraint.IsLooping() {
			flippedTable, flipped, err := virtualFKConstraint(tables, childPart, parentPart)
			if err != nil {
				log.Error().Str("left", virtualJoin.Right.Table).Str("right", virtualJoin.Left.Table).Str("func", "AddVirtualFKs").Err(err).Msg("could not add a (flipped) virtual foreign key, skipping")
				return errors.Wrap(err, "AddVirtualFKs")
			}
			if flippedTable == nil || flipped.IsLooping() {
				log.Debug().Str("left", virtualJoin.Left.Table).Str("right", virtualJoin.Right.Table).Str("func", "AddVirtualFKs").Msg("could not add a virtual foreign key without creating a loop, skipping")
				continue
			}
			table, constraint = flippedTable, flipped
		}

		table.Constraints = append(table.Constraints, constraint)

		log.Debug().Str("left", virtualJoin.Left.Table).Str("right", virtualJoin.Right.Table).Str("func", "AddVirtualFKs").Msg("virtual foreign key added")
	}

	return nil
}
