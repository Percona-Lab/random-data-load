package generate

import (
	"database/sql"
	"math"
	"testing"

	"github.com/Percona-Lab/random-data-load/db"
)

// A page holds a whole number of rows and a tuple is a whole number of
// alignment boundaries wide, so a target page count is not always reachable.
// What is asked of the arithmetic is that it lands as close as the geometry
// allows and says where it landed.
func TestBytesPerRowForPagesLandsClose(t *testing.T) {
	tests := []struct {
		rows, pages int64
	}{
		{rows: 20000, pages: 527},
		{rows: 500000, pages: 4000},
		{rows: 3600000, pages: 44053},
		{rows: 1000, pages: 1000},
		{rows: 100, pages: 3},
	}

	for _, test := range tests {
		width, reached := BytesPerRowForPages(test.rows, test.pages)
		if width <= 0 {
			t.Errorf("%d rows over %d pages gave no width", test.rows, test.pages)
			continue
		}
		if got := PagesForBytesPerRow(test.rows, width); got != reached {
			t.Errorf("%d rows at %d bytes/row: reported %d pages, gives %d", test.rows, width, reached, got)
		}

		// the neighbouring widths are the only other candidates, and neither
		// may be closer than the one that was picked
		for _, other := range []int64{width - heapAlignment, width + heapAlignment} {
			if other <= 0 {
				continue
			}
			if distance(PagesForBytesPerRow(test.rows, other), test.pages) < distance(reached, test.pages) {
				t.Errorf("%d rows over %d pages: picked %d bytes/row (%d pages), %d bytes/row is closer",
					test.rows, test.pages, width, reached, other)
			}
		}
	}
}

func distance(a, b int64) int64 {
	if a > b {
		return a - b
	}
	return b - a
}

// A page count reachable exactly has to be reached exactly. 20,000 rows of
// 180 bytes measured at 527 pages on a real postgres, and the width handed
// back has to land on the same page count -- it may be a few bytes wider,
// since every width sharing a tuple boundary costs the same number of pages
// and the wider one wastes less of the row on padding.
func TestBytesPerRowForPagesIsExactWhenItCanBe(t *testing.T) {
	width, reached := BytesPerRowForPages(20000, 527)
	if reached != 527 {
		t.Fatalf("20000 rows over 527 pages reached %d pages at %d bytes/row", reached, width)
	}
	if width < 180 || width > 187 {
		t.Errorf("20000 rows over 527 pages = %d bytes/row, the table measured at 180", width)
	}
}

func TestPagesThatCannotBeReached(t *testing.T) {
	if width, pages := BytesPerRowForPages(0, 10); width != 0 || pages != 0 {
		t.Errorf("no rows gave %d bytes/row over %d pages", width, pages)
	}
	// 100 rows cannot be spread over 200 pages however wide they are: one row
	// per page is the limit, and the caller is told so by the page count it
	// gets back rather than by a silent zero
	if _, pages := BytesPerRowForPages(100, 200); pages != 100 {
		t.Errorf("100 rows over 200 pages reached %d pages, one row per page is 100", pages)
	}
}

func TestVarlenaWidth(t *testing.T) {
	// the short header holds a length below 127, anything longer needs four
	// bytes for it
	for _, test := range []struct{ length, want int64 }{{0, 1}, {10, 11}, {126, 127}, {127, 131}, {1000, 1004}} {
		if got := varlenaWidth(test.length); got != test.want {
			t.Errorf("varlenaWidth(%d) = %d, want %d", test.length, got, test.want)
		}
	}
}

// A column that cannot take its share hands what is left back to the others,
// instead of the target quietly falling short.
func TestShareOutRedistributesWhatAColumnCannotHold(t *testing.T) {
	fillable := []int{0, 1}
	caps := map[int]float64{0: 13, 1: 2001} // char(12) and a text column
	natural := []float64{13, 13}

	shares := shareOut(400, fillable, caps, natural, 26)

	if shares[0] != 13 {
		t.Errorf("the narrow column took %g bytes, it can only hold 13", shares[0])
	}
	if total := shares[0] + shares[1]; total != 400 {
		t.Errorf("the two columns carry %g bytes between them, the budget was 400", total)
	}
}

// A width target is what the rows take on average, and a column that is NULL
// on some of them only carries what is written into it on the others. Aiming
// at the target without accounting for that leaves the table short by each
// column's null fraction -- which a --stat-file run, whose null fractions are
// all measured ones, would hit on every column at once.
func TestFillerAccountsForTheRowsAColumnIsMissingFrom(t *testing.T) {
	fields := []db.Field{
		{ColumnName: "c1", DataType: "int"},
		{ColumnName: "c2", DataType: "text"},
	}
	// c1 is 4 bytes on every row, c2 holds 40 bytes on half of them
	natural := []float64{4, 20}
	written := []float64{1, 0.5}

	in := &Insert{table: &db.Table{Name: "t1"}, maxTextSize: 65535, widthTarget: &rowWidthTarget{bytesPerRow: 104}}
	lengths := in.distributeFiller(fields, natural, written)

	length, ok := lengths["c2"]
	if !ok {
		t.Fatalf("c2 was left out of the filling: %v", lengths)
	}
	// c2 carries the 100 bytes c1 leaves it, on the half of the rows it is
	// there for, so each of those values has to hold twice that
	if got := float64(varlenaWidth(length)) * written[1]; math.Abs(got-100) > 1 {
		t.Errorf("c2 was filled to %d bytes, which averages %g over its rows, want 100", length, got)
	}
}

// The same correction the other way round: what a column can hold is also
// spread over the rows it is missing from, or a narrow column looks roomier
// than it is and the budget handed to it is never written anywhere.
func TestAColumnsCapIsSpreadOverTheRowsItIsMissingFrom(t *testing.T) {
	fields := []db.Field{
		{ColumnName: "c1", DataType: "varchar", CharacterMaximumLength: sql.NullInt64{Int64: 12, Valid: true}},
		{ColumnName: "c2", DataType: "text"},
	}
	natural := []float64{6, 20}
	written := []float64{0.5, 1}

	in := &Insert{table: &db.Table{Name: "t1"}, maxTextSize: 65535, widthTarget: &rowWidthTarget{bytesPerRow: 200}}
	lengths := in.distributeFiller(fields, natural, written)

	if lengths["c1"] != 12 {
		t.Errorf("varchar(12) was filled to %d, it holds 12 characters", lengths["c1"])
	}
	// filled to the brim, c1 still only averages 6.5 bytes a row, so the rest
	// of the target is c2's to carry
	got := float64(varlenaWidth(lengths["c1"]))*written[0] + float64(varlenaWidth(lengths["c2"]))*written[1]
	if math.Abs(got-200) > 2 {
		t.Errorf("the two columns average %g bytes a row between them, the target was 200", got)
	}
}

// A column that came out NULL on every row carries nothing whatever is written
// into it, so the budget has to go to the columns that do hold something.
func TestAColumnThatIsAlwaysNullCarriesNothing(t *testing.T) {
	fields := []db.Field{
		{ColumnName: "c1", DataType: "text"},
		{ColumnName: "c2", DataType: "text"},
	}
	natural := []float64{0, 20}
	written := []float64{0, 1}

	in := &Insert{table: &db.Table{Name: "t1"}, maxTextSize: 65535, widthTarget: &rowWidthTarget{bytesPerRow: 100}}
	lengths := in.distributeFiller(fields, natural, written)

	if _, ok := lengths["c1"]; ok {
		t.Errorf("c1 is NULL on every row, filling it to %d changes nothing", lengths["c1"])
	}
	if got := varlenaWidth(lengths["c2"]); math.Abs(float64(got)-100) > 1 {
		t.Errorf("c2 carries %d bytes, it has the whole 100 to itself", got)
	}
}
