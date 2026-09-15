package generate

import (
	"math"
	"strconv"
	"strings"

	"github.com/Percona-Lab/random-data-load/db"
	"github.com/rs/zerolog/log"
)

// How postgres lays a heap out. The arithmetic below turns a page count into a
// row width and back, which is the whole point of --target-relpages: page
// count decides scan costs, scan costs decide the plan, and a row width is the
// only handle this tool has on a page count.
const (
	heapPageSize    = 8192
	heapPageHeader  = 24 // PageHeaderData, before the first line pointer
	heapLinePointer = 4  // one ItemIdData per row, at the top of the page
	heapTupleHeader = 24 // 23 bytes of HeapTupleHeaderData, aligned to 8
	heapAlignment   = 8  // MAXALIGN: a stored tuple is rounded up to this

	// A tuple wider than this has postgres compress its widest varlena column
	// or push it out of line into a TOAST table, and the heap row then stops
	// growing with the target. Random filler does not compress, so out of line
	// is where it goes, and the page count stops following.
	toastTupleThreshold = 2000
)

// calibrationRows is how many rows are generated and measured before the run
// starts, to find out how wide a row comes out on its own. Wide enough that a
// column whose values vary in length averages out, small enough that nobody
// notices the sampling queries it costs.
const calibrationRows = 200

// minFilledLength keeps a filled column from being emptied altogether when the
// target is narrower than the columns naturally are.
const minFilledLength = 1

// fixedWidths is what one value of these types takes once stored, which has
// nothing to do with how long it is written out: an int rendered as "1234567"
// is seven characters and four bytes.
var fixedWidths = map[string]int64{
	"bool": 1, "boolean": 1,
	"tinyint": 1, "smallint": 2, "mediumint": 3, "year": 2,
	"int": 4, "integer": 4, "date": 4,
	"bigint": 8, "float": 8, "double": 8,
	"time": 8, "datetime": 8, "timestamp": 8,
	"uuid": 16,
}

// fillableTypes are the ones a target can grow or shrink. They are the types
// whose values this tool writes as text of whatever length it likes, which is
// the same list the string generator answers for.
var fillableTypes = map[string]bool{
	"char": true, "varchar": true,
	"text": true, "tinytext": true, "mediumtext": true, "longtext": true,
	"blob": true, "tinyblob": true, "mediumblob": true, "longblob": true,
}

// BytesPerRowForPages turns a target page count into the row width that comes
// closest to producing it, and says which page count that width actually
// reaches.
//
// A page holds its header, then one line pointer per row, then the rows
// themselves, each carrying a tuple header and rounded up to the alignment
// boundary. What is left over is the room the columns have, which is the
// figure --target-bytes-per-row takes, so the two options meet here.
//
// The two are not a clean inverse of each other. A page holds a whole number
// of rows and a tuple is a whole number of alignment boundaries wide, so only
// some page counts are reachable at all: 500,000 rows fit in 3,677 pages or in
// 4,167, and in nothing between. Hence the second return value, which is what
// the caller should report rather than what it asked for.
//
// It is postgres geometry. InnoDB organises a table by its primary key, fills
// its pages to fifteen sixteenths, and reports a size it sampled rather than
// counted, so the same arithmetic would not mean anything there.
//
// Per-column alignment padding is not modelled: a row of one int and one text
// column wastes nothing, a row alternating int2 and int8 wastes a few bytes
// per row, and the filler columns dominate either way.
func BytesPerRowForPages(rows, pages int64) (bytesPerRow, reachedPages int64) {
	if rows <= 0 || pages <= 0 {
		return 0, 0
	}

	// Every reachable width, walked from the narrowest row upwards. There are
	// at most a thousand of them, one per alignment boundary that fits in a
	// page, so there is nothing to be clever about.
	best, bestPages, bestDistance := int64(0), int64(0), int64(math.MaxInt64)
	for tuple := int64(heapTupleHeader + heapAlignment); tuple+heapLinePointer <= heapPageSize-heapPageHeader; tuple += heapAlignment {
		width := tuple - heapTupleHeader
		reached := PagesForBytesPerRow(rows, width)
		distance := reached - pages
		if distance < 0 {
			distance = -distance
		}
		// a tie goes to the wider row: a table that is too small understates
		// every scan cost built on it, which is the failure that looks like
		// success
		if distance <= bestDistance {
			best, bestPages, bestDistance = width, reached, distance
		}
	}
	return best, bestPages
}

// PagesForBytesPerRow is the other direction: what a row of this width costs
// in pages.
func PagesForBytesPerRow(rows, bytesPerRow int64) int64 {
	if rows <= 0 || bytesPerRow <= 0 {
		return 0
	}
	tuple := bytesPerRow + heapTupleHeader
	if remainder := tuple % heapAlignment; remainder != 0 {
		tuple += heapAlignment - remainder
	}
	rowsPerPage := math.Floor(float64(heapPageSize-heapPageHeader) / float64(tuple+heapLinePointer))
	if rowsPerPage < 1 {
		rowsPerPage = 1
	}
	return int64(math.Ceil(float64(rows) / rowsPerPage))
}

// storageWidth estimates what one generated value takes in a stored row.
//
// A fixed-width type takes what its type takes whatever was drawn for it, and
// a NULL takes nothing at all -- it lives in the tuple's null bitmap. Only a
// variable-length value is measured, and it carries a length header of one
// byte, or four once it is long enough that the short form cannot hold its
// length.
func storageWidth(field db.Field, value Getter) int64 {
	text, known := valueText(value)
	if known && text == NULL {
		return 0
	}
	if width, fixed := fixedWidths[field.DataType]; fixed {
		return width
	}
	if !known {
		// a column left to the engine's DEFAULT, whose value this side never
		// sees. Its type is the only thing left to go on.
		return varlenaWidth(int64(field.CharacterMaximumLength.Int64) / 2)
	}

	length := int64(len(text))
	if field.DataType == "decimal" || field.DataType == "numeric" {
		// a numeric holds four decimal digits per two bytes, plus a header
		return 8 + 2*((length+3)/4)
	}
	return varlenaWidth(length)
}

func varlenaWidth(length int64) int64 {
	if length < 127 {
		return length + 1
	}
	return length + 4
}

// valueText reads back what was generated for a column, without the quotes the
// INSERT will wrap it in. It reports false for a value this side cannot see,
// which is a column left to the engine's own DEFAULT.
func valueText(value Getter) (string, bool) {
	wrapper, ok := value.(*GetterWrapper)
	if ok {
		if wrapper == nil || wrapper.Elem == nil {
			return "", false
		}
		value = wrapper.Elem
	}
	if _, isDefault := value.(*DefaultKeyword); isDefault {
		return "", false
	}
	return value.String(), true
}

// rowWidthTarget is what one table was asked to come out at, and what the
// calibration pass worked out has to be written into each column to get there.
type rowWidthTarget struct {
	bytesPerRow int64
	lengths     map[string]int64 // column name, lowered, to the length to fill it to
}

// lengthFor returns how long this column's value has to be, and whether it is
// one of the columns carrying the target at all.
func (t *rowWidthTarget) lengthFor(column string) (int64, bool) {
	if t == nil {
		return 0, false
	}
	length, ok := t.lengths[strings.ToLower(column)]
	return length, ok
}

// calibrate measures how wide a row comes out on its own, then works out how
// long each free-text column has to be written to reach the target.
//
// Solving for a row width by hand is a load, a measurement, an adjustment and
// a reload, and every run of the study did it at least once. The measurement
// does not need the database to have been written to: generating a few hundred
// rows and adding up what they would take answers the same question in a
// fraction of a second, so the loop happens here instead.
func (in *Insert) calibrate() error {
	if in.widthTarget == nil || in.widthTarget.bytesPerRow <= 0 {
		return nil
	}

	fields, values, err := in.buildValues(calibrationRows)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		return nil
	}

	natural := make([]float64, len(fields))
	for _, row := range values {
		for column, value := range row {
			natural[column] += float64(storageWidth(fields[column], value))
		}
	}
	for column := range natural {
		natural[column] /= float64(len(values))
	}

	in.widthTarget.lengths = in.distributeFiller(fields, natural)
	return nil
}

// distributeFiller shares the target width out over the columns that can carry
// it.
//
// The share each column gets is proportional to how wide it already is, so a
// table whose free text is one short label and one long description keeps that
// shape instead of flattening to two equal columns. A column that cannot take
// its share -- char(8) has nowhere to put 400 bytes -- is pinned at what it can
// hold and its remainder goes back to the others.
func (in *Insert) distributeFiller(fields []db.Field, natural []float64) map[string]int64 {
	target := in.widthTarget.bytesPerRow

	fillable := []int{}
	caps := map[int]float64{}
	var fixed, fillableNatural float64
	for column, field := range fields {
		if !in.fillable(field) {
			fixed += natural[column]
			continue
		}
		fillable = append(fillable, column)
		fillableNatural += natural[column]
		caps[column] = float64(varlenaWidth(in.maxLengthOf(field)))
	}

	if len(fillable) == 0 {
		log.Warn().Str("table", in.table.Name).Int64("target", target).
			Msgf("%s has no text column this run generates, so its row width cannot be aimed at %d bytes. Only char, varchar, text and blob columns can be grown or shrunk", in.table.Name, target)
		return nil
	}

	budget := float64(target) - fixed
	var ceiling float64
	for _, column := range fillable {
		ceiling += caps[column]
	}
	if budget > ceiling {
		log.Warn().Str("table", in.table.Name).Int64("target", target).
			Msgf("%s cannot reach %d bytes per row: its %d fillable columns hold at most %d bytes between them, on top of %d bytes of fixed-width columns. Widen a column, add one, or raise --max-text-size",
				in.table.Name, target, len(fillable), int64(ceiling), int64(fixed))
		budget = ceiling
	}

	shares := shareOut(budget, fillable, caps, natural, fillableNatural)

	lengths := map[string]int64{}
	var total float64 = fixed
	for _, column := range fillable {
		total += shares[column]
		lengths[strings.ToLower(fields[column].ColumnName)] = lengthForWidth(shares[column])
	}

	in.reportCalibration(fields, natural, lengths, int64(math.Round(total)))
	return lengths
}

// shareOut splits the budget over the fillable columns, pinning the ones that
// cannot take their share and handing what they could not take to the rest.
// A handful of passes settles it: each one either places everything or pins at
// least one more column.
func shareOut(budget float64, fillable []int, caps map[int]float64, natural []float64, fillableNatural float64) map[int]float64 {
	shares := map[int]float64{}
	for _, column := range fillable {
		if fillableNatural > 0 {
			shares[column] = budget * natural[column] / fillableNatural
			continue
		}
		shares[column] = budget / float64(len(fillable))
	}

	floor := float64(varlenaWidth(minFilledLength))
	for pass := 0; pass < len(fillable)+1; pass++ {
		var slack float64
		free := []int{}
		for _, column := range fillable {
			switch {
			case shares[column] > caps[column]:
				slack += shares[column] - caps[column]
				shares[column] = caps[column]
			case shares[column] < floor:
				slack -= floor - shares[column]
				shares[column] = floor
			default:
				free = append(free, column)
			}
		}
		if slack == 0 || len(free) == 0 {
			break
		}
		for _, column := range free {
			shares[column] += slack / float64(len(free))
		}
	}
	return shares
}

// lengthForWidth inverts the varlena header: the caller asked for a column of
// this many bytes, and the value written into it has to be that much minus
// what the length header takes.
func lengthForWidth(width float64) int64 {
	length := int64(math.Round(width)) - 1
	if length >= 127 {
		length = int64(math.Round(width)) - 4
	}
	if length < minFilledLength {
		return minFilledLength
	}
	return length
}

// fillable reports whether this column is one the target can be written into:
// a text column this run generates itself. A column sampled from a parent
// holds that parent's value, and a column given a fixed value by
// --values-freq-map or by a query literal holds what it was told to.
func (in *Insert) fillable(field db.Field) bool {
	if field.Skip || field.IsGenerated || !fillableTypes[field.DataType] {
		return false
	}
	if in.table.IsFieldInAnyConstraints(field) {
		return false
	}
	if freq, ok := in.frequencies[field.ColumnName]; ok && len(freq.IndexValues) > 0 {
		return false
	}
	return true
}

// maxLengthOf is how long a value this column can hold, in characters.
func (in *Insert) maxLengthOf(field db.Field) int64 {
	maximum := in.maxTextSize
	if field.CharacterMaximumLength.Valid && field.CharacterMaximumLength.Int64 > 0 && field.CharacterMaximumLength.Int64 < maximum {
		maximum = field.CharacterMaximumLength.Int64
	}
	return maximum
}

func (in *Insert) reportCalibration(fields []db.Field, natural []float64, lengths map[string]int64, reached int64) {
	var before float64
	for _, width := range natural {
		before += width
	}

	filled := []string{}
	for _, field := range fields {
		if length, ok := lengths[strings.ToLower(field.ColumnName)]; ok {
			filled = append(filled, field.ColumnName+"="+strconv.FormatInt(length, 10))
		}
	}

	log.Info().Str("table", in.table.Name).
		Int64("naturalBytesPerRow", int64(math.Round(before))).
		Int64("targetBytesPerRow", in.widthTarget.bytesPerRow).
		Int64("reachedBytesPerRow", reached).
		Strs("filledTo", filled).
		Msgf("%s comes out at %d bytes per row on its own; filling %s to reach %d, which lands on %d",
			in.table.Name, int64(math.Round(before)), strings.Join(filled, ", "), in.widthTarget.bytesPerRow, reached)

	if reached > toastTupleThreshold {
		log.Warn().Str("table", in.table.Name).
			Msgf("%d bytes per row puts the tuple over the %d bytes postgres starts pushing a column out of line at, and random filler does not compress, so the heap will stay narrow and the page count will not follow. ALTER TABLE ... ALTER COLUMN ... SET STORAGE PLAIN on the filled columns keeps them in the heap",
				reached, toastTupleThreshold)
	}
}
