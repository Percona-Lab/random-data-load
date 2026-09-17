package generate

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/Percona-Lab/random-data-load/db"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// A unique key spread over several foreign keys cannot be filled one key at a
// time. Each sampler walks its own parent, each one is perfectly well behaved
// on its own column, and the combinations they produce start repeating as soon
// as the shortest walk comes round again: filling inventory(warehouse_id,
// product_id) from 100 warehouses and 4,000 products repeats a pair every
// 4,000 rows, whatever either sampler does.
//
// So the columns of such a key are filled together, by walking the cross
// product of their parents. Row n of the child takes the combination at
// position n of that product, which is unique until every combination has been
// used, and only repeats after that.
//
// The row's position is read like an odometer: the last parent advances every
// row, the one before it advances once the last has been all the way round,
// and so on. Weights are the products of the sizes to the right, exactly as
// digits in a mixed radix.

// compositeKeyCursor is the walk itself, shared by every bulk of the run: the
// samplers are rebuilt per bulk, the position is not.
type compositeKeyCursor struct {
	mutex    sync.Mutex
	next     int64
	capacity int64
	wrapped  bool
}

// reserve hands the caller the next stretch of positions.
func (c *compositeKeyCursor) reserve(count int64) int64 {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	start := c.next
	c.next += count
	return start
}

// exhausted reports whether the walk has gone past the end of the cross
// product, which is where the combinations start repeating and a unique key
// starts refusing rows. Using the last combination is not going past it, so a
// run that fills the product exactly says nothing. It is worth saying once.
func (c *compositeKeyCursor) exhausted(position int64) bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if position <= c.capacity || c.wrapped {
		return false
	}
	c.wrapped = true
	return true
}

var storedCompositeKeyCursors = map[string]*compositeKeyCursor{}
var storedCompositeKeyCursorsMutex sync.Mutex

var usableRowCounts = map[string]int64{}
var usableRowCountsMutex sync.Mutex

// parentUsableRowCount counts the rows of a parent that can fill this key,
// once per run. A sampler is rebuilt for every bulk and a parent is fully
// inserted before anything pointing at it is, so counting it again per bulk
// would be the same answer at the price of a full scan each time.
func parentUsableRowCount(schema, table string, fields []db.Field) (int64, error) {
	key := schema + "." + table + "/" + db.EscapedNamesListFromFields(fields)

	usableRowCountsMutex.Lock()
	defer usableRowCountsMutex.Unlock()

	if count, ok := usableRowCounts[key]; ok {
		return count, nil
	}
	count, err := db.CountNonNullRows(schema, table, fields)
	if err != nil {
		return 0, err
	}
	usableRowCounts[key] = count
	log.Debug().Str("table", table).Str("schema", schema).Int64("rows", count).Msg("counted the rows of a parent a composite key can point at")
	return count, nil
}

// compositeKeyPart is one of the foreign keys of the key, and the parent it
// reads.
type compositeKeyPart struct {
	sampleCommon
	weight int64
}

// CompositeKeySample fills the columns of a unique key from several parents at
// once, so that the key as a whole stays unique.
type CompositeKeySample struct {
	parts  []*compositeKeyPart
	cursor *compositeKeyCursor
	child  string
	key    string
}

func (s *CompositeKeySample) Sample() error {
	rows := int64(len(s.parts[0].values))
	if rows == 0 {
		return nil
	}

	start := s.cursor.reserve(rows)
	if s.cursor.exhausted(start + rows) {
		log.Warn().Str("table", s.child).Str("key", s.key).Int64("combinations", s.cursor.capacity).
			Msgf("the parents of %s can make %d different combinations and all of them have been used, so every row from here on repeats one and %s will refuse it",
				s.key, s.cursor.capacity, s.child)
	}

	// every row of the bulk takes a combination, so the rows each part fills
	// are all of them, in order; the same list serves every part
	targets := make([]int, rows)
	for row := range targets {
		targets[row] = row
	}

	for _, part := range s.parts {
		offsets := make([]int64, rows)
		for row := int64(0); row < rows; row++ {
			position := (start + row) % s.cursor.capacity
			offsets[row] = (position / part.weight) % part.tableSize
		}
		if err := part.fillFromRowNumbers(targets, offsets); err != nil {
			return err
		}
	}
	return nil
}

// newCompositeKeySample builds the walk for one child table's unique key.
//
// The parents are ordered smallest first, so the largest is the one advancing
// every row: a run that stops well short of the whole cross product then still
// sees most of the big parent's rows, rather than the first few of it over and
// over.
func (in *Insert) newCompositeKeySample(constraints db.Constraints, key []string, values map[*db.Constraint][][]Getter) (*CompositeKeySample, error) {
	sample := &CompositeKeySample{
		child: in.table.Schema + "." + in.table.Name,
		key:   "(" + strings.Join(key, ", ") + ")",
	}

	for _, constraint := range constraints {
		size, err := parentUsableRowCount(constraint.ReferencedTableSchema, constraint.ReferencedTableName, constraint.ReferencedFields)
		if err != nil {
			return nil, err
		}
		if size == 0 {
			return nil, errors.Errorf("table %s.%s holds no row this key can point at, so there is nothing to build %s from. Insert into it first, and check --rows if this run was meant to fill it",
				constraint.ReferencedTableSchema, constraint.ReferencedTableName, sample.key)
		}

		part := &compositeKeyPart{}
		part.Init(constraint.ReferencedFields, constraint.ReferencedTableSchema, constraint.ReferencedTableName,
			constraint.ConstraintName, values[constraint], size, &in.fklinks)
		sample.parts = append(sample.parts, part)
	}
	sort.SliceStable(sample.parts, func(i, j int) bool { return sample.parts[i].tableSize < sample.parts[j].tableSize })

	capacity := int64(1)
	for i := len(sample.parts) - 1; i >= 0; i-- {
		sample.parts[i].weight = capacity
		capacity = saturatingProduct(capacity, sample.parts[i].tableSize)
	}

	cursor, err := in.compositeKeyCursor(sample, capacity)
	if err != nil {
		return nil, err
	}
	sample.cursor = cursor
	return sample, nil
}

// compositeKeyCursor finds or starts the walk for this key, and refuses the
// run when what was asked for cannot be done at all.
//
// The refusal comes here rather than up front because the parents are filled
// by this same run: their sizes are only known once they are. It is still
// before the first row of the child is written, which is what matters -- the
// alternative is the engine rejecting a duplicate key somewhere in the middle,
// with part of the table already loaded.
func (in *Insert) compositeKeyCursor(sample *CompositeKeySample, capacity int64) (*compositeKeyCursor, error) {
	storedCompositeKeyCursorsMutex.Lock()
	defer storedCompositeKeyCursorsMutex.Unlock()

	name := sample.child + "/" + sample.key
	if cursor, ok := storedCompositeKeyCursors[name]; ok {
		return cursor, nil
	}

	sizes := []string{}
	for _, part := range sample.parts {
		sizes = append(sizes, fmt.Sprintf("%s=%d", part.table, part.tableSize))
	}
	if in.rowsWanted > capacity {
		return nil, errors.Errorf("%s asks for %d rows, and its unique key %s can only be filled with %d different values: its parents hold %s rows between them. Fill the parents with more rows, or ask this table for fewer",
			sample.child, in.rowsWanted, sample.key, capacity, strings.Join(sizes, ", "))
	}

	log.Info().Str("table", sample.child).Str("key", sample.key).Int64("combinations", capacity).Strs("parents", sizes).
		Msgf("%s of %s is filled from %d parents at once, walking their combinations, so the key stays unique. The relationship sampling options do not apply to it",
			sample.key, sample.child, len(sample.parts))

	cursor := &compositeKeyCursor{capacity: capacity}
	storedCompositeKeyCursors[name] = cursor
	return cursor, nil
}

// saturatingProduct multiplies without wrapping round into a negative number.
// Three parents of a million rows are 10^18 combinations, which is close
// enough to the end of an int64 to be worth watching, and a capacity that
// large is no capacity at all.
func saturatingProduct(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}
