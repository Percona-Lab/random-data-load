package generate

import (
	"fmt"
	"math"
	"strings"

	"github.com/Percona-Lab/random-data-load/db"
	"github.com/pkg/errors"
)

const (
	// DefaultSelfFKRoots and DefaultSelfFKDepth are the tree a
	// self-referencing table got before either could be set: half its rows
	// roots, the other half pointing at them.
	DefaultSelfFKRoots = 0.5
	DefaultSelfFKDepth = 2
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
// being inserted takes its parents from.
type SelfReferencingLevel struct {
	// PreviousStart and PreviousEnd count the rows the table held when the
	// level before this one started, and when this one did. Everything
	// before PreviousEnd was there before this level, and is all it may
	// point at.
	PreviousStart, PreviousEnd int64

	// Key is the column the database numbers as rows come in. The level
	// before this one holds the rows whose Key is above After and up to
	// UpTo, the largest values the table held when that level and this one
	// started. After is empty when the table was empty before that level.
	// Key is empty when the table has no such column, and its levels cannot
	// be told apart.
	Key, After, UpTo string
}

// keyRange is the condition keeping a sample on the level before this one,
// for a key pointing at these columns.
func (l SelfReferencingLevel) keyRange(fields []db.Field) string {
	if l.Key == "" || l.UpTo == "" || len(fields) != 1 || !strings.EqualFold(fields[0].ColumnName, l.Key) {
		return ""
	}
	key := db.Escape(fields[0].ColumnName)
	condition := " AND " + key + " <= " + l.UpTo
	if l.After != "" {
		condition += " AND " + key + " > " + l.After
	}
	return condition
}

// SetSelfReferencingLevel makes the keys this table has on itself point at
// the level before this one, rather than at the whole table.
func (in *Insert) SetSelfReferencingLevel(level *SelfReferencingLevel) {
	in.selfLevel = level
}

// LevelSample samples a self-referencing key's parents by coin flip, as
// --binomial does, out of the level before the one being inserted only.
// Every row then sits exactly as many levels down as it was inserted at, so
// a recursive query walks exactly --self-fk-depth levels.
//
// The level is told apart by its key, which the database numbered as its
// rows came in. Reading rows by their position instead takes numbering the
// whole table for every sample, which is what made a large tree slow.
type LevelSample struct {
	sampleCommon
	coinFlipPercent float64
	keyRange        string
}

func NewLevelSample(fields []db.Field, schema, tablename, constraintName string, values [][]Getter, level SelfReferencingLevel, fkCli *ForeignKeyLinks) Sampler {
	s := &LevelSample{keyRange: level.keyRange(fields)}

	// The coin flip is guarded against the rows it can bring back: the
	// level's own when it is told apart, everything before it otherwise.
	candidates := level.PreviousEnd
	if s.keyRange != "" {
		candidates = level.PreviousEnd - level.PreviousStart
	}
	s.Init(fields, schema, tablename, constraintName, values, candidates, fkCli)
	s.coinFlipPercent = s.guardedCoinFlipPercent(fkCli.CoinFlipPercent.For(tablename))
	return s
}

func (s *LevelSample) Sample() error {
	query := fmt.Sprintf("SELECT %s FROM %s.%s %s AND %s%s ORDER BY 1 LIMIT %d",
		db.EscapedNamesListFromFields(s.fields), db.Escape(s.schema), db.Escape(s.table),
		db.BinomialWhereClause(s.coinFlipPercent), db.EscapedFieldsIsNotNull(s.fields), s.keyRange, s.limit)

	return s.query(query, s.values)
}

// SelfReferencingKey is the column a self-referencing table's levels can be
// told apart by: the one its keys on itself point at, when the database
// numbers it as rows come in. A key the run generates itself is random, and
// the rows of every level end up mixed together.
func SelfReferencingKey(table *db.Table) (string, bool) {
	var key string
	for _, c := range table.Constraints {
		if !c.IsSelfReferencing() {
			continue
		}
		if len(c.ReferencedFields) != 1 || !c.ReferencedFields[0].AutoIncrement {
			return "", false
		}
		if key != "" && !strings.EqualFold(key, c.ReferencedFields[0].ColumnName) {
			return "", false
		}
		key = c.ReferencedFields[0].ColumnName
	}
	return key, key != ""
}

// selfReferencingSampler picks the sampler for a key this table has on
// itself, when its rows are being inserted level by level.
//
// The level sampler is used unless the relationship was named explicitly, in
// --binomial, --sequential, --normal or --pareto: that is a choice made for
// this very key, where --default-relationship is one made for every key and
// knows nothing of levels. A sampler named that way still only sees the rows
// that were there before this level, through the parent size it is given.
func (in *Insert) selfReferencingSampler(constraint *db.Constraint, values [][]Getter) Sampler {
	level := *in.selfLevel
	if explicit, named := in.fklinks.namedRelationship(constraint.ReferencedTableName, in.table.Name); named {
		return explicit(constraint.ReferencedFields, constraint.ReferencedTableSchema, constraint.ReferencedTableName, constraint.ConstraintName, values, level.PreviousEnd, &in.fklinks)
	}
	return NewLevelSample(constraint.ReferencedFields, constraint.ReferencedTableSchema, constraint.ReferencedTableName, constraint.ConstraintName, values, level, &in.fklinks)
}
