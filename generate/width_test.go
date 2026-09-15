package generate

import "testing"

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
