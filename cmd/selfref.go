package cmd

import (
	"fmt"
	"strings"

	"github.com/Percona-Lab/random-data-load/db"
	"github.com/Percona-Lab/random-data-load/generate"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// selfReferencingPlan is how the rows of a table pointing at itself are
// spread over the levels of its tree. The copy of the table inserts the
// roots, level 0, and the table itself every level after that, each pointing
// at the one before.
type selfReferencingPlan struct {
	levels []int64

	// starts[k] is how many rows the table held when level k started, which
	// is where its rows begin once the table is sorted by its key.
	starts []int64

	// roots is the copy of the table inserting level 0.
	roots *db.Table

	// rows is what --rows asked for the table as a whole, the figure a page
	// count target is spread over.
	rows int64
}

// planSelfReferencingLevels works out the levels of a table that points at
// itself, from --self-fk-roots and --self-fk-depth.
func (cmd *RunCmd) planSelfReferencingLevels(table, roots *db.Table) (*selfReferencingPlan, error) {
	rows := cmd.Rows.For(table.Name)
	depth := int64(generate.DefaultSelfFKDepth)
	if cmd.SelfFKDepth.IsSetFor(table.Name) {
		depth = cmd.SelfFKDepth.For(table.Name)
	}
	share, from := cmd.selfReferencingRoots(table)

	levels, err := generate.SelfReferencingLevels(rows, share, depth)
	if err != nil {
		return nil, errors.Wrapf(err, "table %s points at itself", table.Name)
	}

	sizes := make([]string, len(levels))
	for k, n := range levels {
		sizes[k] = fmt.Sprint(n)
	}
	log.Info().Str("table", table.Name).Int64("rows", rows).Int64("depth", depth).Float64("roots", share).Ints64("levels", levels).
		Msgf("%s points at itself, so its %d rows are inserted as a tree of %d levels, roots first: %s. The roots are %g of the rows (%s), and each level mostly points at the one before it. Set --self-fk-roots and --self-fk-depth to change it",
			table.Name, rows, depth, strings.Join(sizes, ", "), share, from)

	return &selfReferencingPlan{
		levels: levels,
		starts: make([]int64, len(levels)),
		roots:  roots,
		rows:   rows,
	}, nil
}

// selfReferencingRoots is the share of a self-referencing table's rows that
// are roots, and where that figure came from.
//
// A root is a row whose key on itself is NULL, and nothing else leaves that
// key NULL, so null_frac of the key column measured on the source is exactly
// the share of roots it had. It comes after the flag, as the rest of a dump
// does: a flag is something the caller asked for by hand.
func (cmd *RunCmd) selfReferencingRoots(table *db.Table) (float64, string) {
	if cmd.SelfFKRoots.IsSetFor(table.Name) {
		return cmd.SelfFKRoots.For(table.Name), "--self-fk-roots"
	}
	if frac, column, ok := cmd.statRootsShare(table); ok {
		if frac > 0 {
			return frac, fmt.Sprintf("the null_frac of %s.%s in --stat-file", table.Name, column)
		}
		log.Warn().Str("table", table.Name).Str("column", column).
			Msgf("--stat-file says %s.%s is never NULL, so the roots of the source's tree do not have a NULL parent and their share cannot be read from it. Using %g, set --self-fk-roots to change it",
				table.Name, column, generate.DefaultSelfFKRoots)
	}
	return generate.DefaultSelfFKRoots, "the default"
}

// statRootsShare reads the share of roots out of the dump: the null_frac of
// the column a table's key on itself is held in. A row is only a root when
// the whole key is NULL, and the dump says nothing about how the columns of a
// key on several of them are NULL together, so only a key on one column is
// read.
func (cmd *RunCmd) statRootsShare(table *db.Table) (float64, string, bool) {
	var column string
	for _, c := range table.Constraints {
		if !c.IsSelfReferencing() {
			continue
		}
		if column != "" || len(c.ColumnsName) != 1 {
			return 0, "", false
		}
		column = c.ColumnsName[0]
	}
	if column == "" {
		return 0, "", false
	}

	for _, cs := range cmd.stats {
		if !strings.EqualFold(cs.Tablename, table.Name) || !strings.EqualFold(cs.Attname, column) {
			continue
		}
		if cs.Schemaname != "" && table.Schema != "" && !strings.EqualFold(cs.Schemaname, table.Schema) {
			continue
		}
		return cs.NullFrac, column, true
	}
	return 0, "", false
}

// runSelfReferencing inserts one of the two tables a self-referencing table
// was split into: the copy inserts the roots, the table itself every level
// after them.
func (cmd *RunCmd) runSelfReferencing(table *db.Table, plan *selfReferencingPlan) error {
	start, err := db.CountRows(table.Schema, table.Name)
	if err != nil {
		return err
	}

	if table == plan.roots {
		plan.starts[0] = start
		return cmd.insert(table, plan.levels[0], plan.rows, nil, fmt.Sprintf("%s level 0", table.Name))
	}

	previous := 0
	for k := 1; k < len(plan.levels); k++ {
		// A dry run writes nothing, so the table does not grow as the levels
		// go by and they are placed where they would have started.
		if cmd.DryRun {
			start = plan.starts[k-1] + plan.levels[k-1]
		}
		plan.starts[k] = start
		if plan.levels[k] == 0 {
			continue
		}

		level := &generate.SelfReferencingLevel{
			PreviousStart: plan.starts[previous],
			PreviousEnd:   start,
		}
		if err := cmd.insert(table, plan.levels[k], plan.rows, level, fmt.Sprintf("%s level %d", table.Name, k)); err != nil {
			return errors.Wrapf(err, "level %d of %d", k, len(plan.levels)-1)
		}
		previous = k

		if !cmd.DryRun && k < len(plan.levels)-1 {
			start, err = db.CountRows(table.Schema, table.Name)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
