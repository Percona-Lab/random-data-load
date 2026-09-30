package generate

import (
	"math"
	"math/rand"

	"github.com/Percona-Lab/random-data-load/db"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

const (
	// DefaultSelfFKRoots and DefaultSelfFKDepth are the tree a
	// self-referencing table got before either could be set: half its rows
	// roots, the other half pointing at them.
	DefaultSelfFKRoots = 0.5
	DefaultSelfFKDepth = 2

	// selfFKLevelSpread is how many standard deviations of the level sampler
	// fit in the level it aims at, so that about 95% of the draws land in the
	// previous level and the rest spill onto the ones before it.
	selfFKLevelSpread = 4
)

// SelfReferencingLevels spreads the rows of a table pointing at itself over
// depth levels, level 0 being the roots and every other level pointing at the
// one before it.
//
// Each level is the one before it times the same factor, which is picked so
// that the roots take their share and the levels add up to the rows asked
// for. How the tree looks follows from how the share compares to 1/depth:
// below it the levels grow, an org chart or a category tree; at it they are
// all the same size; above it they shrink, a comment thread whose replies
// peter out. At depth 2 and half the rows as roots, which is the default,
// that is the split in two the table always had.
//
// The levels are rounded by their running total, so they add up to rows
// exactly, and there is always a root when there are rows at all: a table
// has to hold something before it can point at itself.
func SelfReferencingLevels(rows int64, roots float64, depth int64) ([]int64, error) {
	if depth < 1 {
		return nil, errors.Errorf("--self-fk-depth is %d, a tree needs at least 1 level", depth)
	}
	if depth == 1 {
		// every row a root: nothing points at anything
		return []int64{rows}, nil
	}
	if roots <= 0 || roots > 1 {
		return nil, errors.Errorf("--self-fk-roots is %g, which is not a fraction above 0 and at most 1. The roots are what the other levels point at, so there has to be some", roots)
	}

	growth := levelGrowth(roots, depth)
	weights := make([]float64, depth)
	var total float64
	for k := range weights {
		weights[k] = math.Pow(growth, float64(k))
		total += weights[k]
	}

	levels := make([]int64, depth)
	var cumulativeWeight float64
	var placed int64
	for k := range levels {
		cumulativeWeight += weights[k]
		upTo := int64(math.Round(float64(rows) * cumulativeWeight / total))
		if k == len(levels)-1 {
			upTo = rows
		}
		if k == 0 && upTo == 0 && rows > 0 {
			upTo = 1
		}
		upTo = max(upTo, placed)
		levels[k] = upTo - placed
		placed = upTo
	}
	return levels, nil
}

// levelGrowth finds the factor g with 1 + g + ... + g^(depth-1) = 1/roots,
// the one making the roots' level that share of the whole tree. The sum only
// grows with g, so halving the interval finds it.
func levelGrowth(roots float64, depth int64) float64 {
	target := 1 / roots
	sum := func(g float64) float64 {
		var s, term float64 = 0, 1
		for k := int64(0); k < depth; k++ {
			s += term
			term *= g
		}
		return s
	}

	low, high := 0.0, 1.0
	for sum(high) < target {
		high *= 2
	}
	for i := 0; i < 200 && high-low > 1e-12*high; i++ {
		mid := (low + high) / 2
		if sum(mid) < target {
			low = mid
		} else {
			high = mid
		}
	}
	return (low + high) / 2
}

// SelfReferencingLevel is where, in a table pointing at itself, the level
// being inserted takes its parents from. Positions count the rows in the
// order of the key they point at, from 0, the way the samplers ask for rows.
type SelfReferencingLevel struct {
	// PreviousStart and PreviousEnd bound the level before this one: rows
	// PreviousStart up to, and not including, PreviousEnd. Everything before
	// PreviousEnd was there before this level started, and is all it may
	// point at.
	PreviousStart, PreviousEnd int64
}

// SetSelfReferencingLevel makes the keys this table has on itself point at
// the level before this one, rather than at the whole table.
func (in *Insert) SetSelfReferencingLevel(level *SelfReferencingLevel) {
	in.selfLevel = level
}

// LevelSample draws a self-referencing key's parents on a bell curve around
// the middle of the level before the one being inserted.
//
// Most of a level's rows get a parent exactly one level up, so a recursive
// query walks about as many levels as were asked for, and the curve's tails
// reach into the levels above that, as a real tree has rows closer to its
// root than others. It never reaches past the level before: those rows are
// the ones being inserted, and pointing at them would make the tree deeper
// than asked.
type LevelSample struct {
	sampleCommon
	mean, stddev float64
}

func NewLevelSample(fields []db.Field, schema, tablename, constraintName string, values [][]Getter, level SelfReferencingLevel, fkCli *ForeignKeyLinks) Sampler {
	s := &LevelSample{}
	s.Init(fields, schema, tablename, constraintName, values, level.PreviousEnd, fkCli)
	width := float64(level.PreviousEnd - level.PreviousStart)
	s.mean = float64(level.PreviousStart) + width/2
	s.stddev = width / selfFKLevelSpread
	return s
}

func (s *LevelSample) Sample() error {
	if s.tableSize <= 0 {
		return errors.Errorf("%s.%s holds no row yet for its key %s to point at", s.schema, s.table, s.constraintName)
	}

	targets := make([]int, len(s.values))
	offsets := make([]int64, len(s.values))
	for row := range s.values {
		targets[row] = row
		offsets[row] = s.draw()
	}
	return s.fillFromRowNumbers(targets, offsets)
}

// draw picks the position of one parent, drawing again whenever the curve
// lands outside the rows that may be pointed at.
func (s *LevelSample) draw() int64 {
	for attempt := 0; attempt < maxNormalDraws; attempt++ {
		position := int64(math.Floor(s.mean + s.stddev*rand.NormFloat64()))
		if position >= 0 && position < s.tableSize {
			return position
		}
	}
	return min(max(int64(s.mean), 0), s.tableSize-1)
}

// keyFollowsInsertOrder reports whether the database numbers these key columns
// as the rows come in, so that the rows of a level are the ones sitting next
// to each other once sorted by it. A key the run generates itself is random,
// and the rows of every level end up mixed together.
func keyFollowsInsertOrder(fields []db.Field) bool {
	return len(fields) == 1 && fields[0].AutoIncrement
}

// selfReferencingSampler picks the sampler for a key this table has on
// itself, when its rows are being inserted level by level.
//
// The level curve is used unless the relationship was named explicitly, in
// --binomial, --sequential, --normal or --pareto: that is a choice made for
// this very key, where --default-relationship is one made for every key and
// knows nothing of levels. A sampler named that way still only sees the rows
// that were there before this level, through the parent size it is given.
func (in *Insert) selfReferencingSampler(constraint *db.Constraint, values [][]Getter) Sampler {
	level := *in.selfLevel
	if explicit, named := in.fklinks.namedRelationship(constraint.ReferencedTableName, in.table.Name); named {
		return explicit(constraint.ReferencedFields, constraint.ReferencedTableSchema, constraint.ReferencedTableName, constraint.ConstraintName, values, level.PreviousEnd, &in.fklinks)
	}

	logOnce("selfFKKeyOrder:"+in.table.FullName()+"/"+constraint.ConstraintName, func() {
		if keyFollowsInsertOrder(constraint.ReferencedFields) {
			return
		}
		log.Warn().Str("table", in.table.Name).Str("constraint", constraint.ConstraintName).
			Msgf("%s points at itself through a key the database does not number as rows come in, so its levels are mixed together once sorted by it and a row can point at any of them, its own included. The tree will not have the --self-fk-depth asked for: an auto-increment, serial or identity key keeps the levels apart", in.table.Name)
	})
	return NewLevelSample(constraint.ReferencedFields, constraint.ReferencedTableSchema, constraint.ReferencedTableName, constraint.ConstraintName, values, level, &in.fklinks)
}
