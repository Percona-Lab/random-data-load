package generate

import (
	"slices"
	"testing"
)

func TestSelfReferencingLevels(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rows  int64
		roots float64
		depth int64
		want  []int64
		err   bool
	}{
		// the split the table always had
		{name: "the default is half and half", rows: 1000, roots: 0.5, depth: 2, want: []int64{500, 500}},
		{name: "a share of 1/depth is flat", rows: 1000, roots: 0.25, depth: 4, want: []int64{250, 250, 250, 250}},
		{name: "few roots fan out", rows: 1000, roots: 0.1, depth: 4, want: []int64{100, 166, 276, 458}},
		{name: "many roots peter out", rows: 1000, roots: 0.7, depth: 3, want: []int64{700, 227, 73}},
		{name: "an org chart", rows: 11111, roots: 1.0 / 11111, depth: 5, want: []int64{1, 10, 100, 1000, 10000}},
		{name: "one level is all roots", rows: 1000, roots: 0.1, depth: 1, want: []int64{1000}},
		{name: "every row a root", rows: 10, roots: 1, depth: 3, want: []int64{10, 0, 0}},
		{name: "there is always a root", rows: 10, roots: 0.001, depth: 3, want: []int64{1, 0, 9}},
		{name: "no rows", rows: 0, roots: 0.5, depth: 2, want: []int64{0, 0}},
		{name: "no roots is refused", rows: 1000, roots: 0, depth: 2, err: true},
		{name: "more than every row is refused", rows: 1000, roots: 1.5, depth: 2, err: true},
		{name: "no level is refused", rows: 1000, roots: 0.5, depth: 0, err: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SelfReferencingLevels(tc.rows, tc.roots, tc.depth)
			if tc.err {
				if err == nil {
					t.Fatalf("expected an error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
			var sum int64
			for _, n := range got {
				sum += n
			}
			if sum != tc.rows {
				t.Errorf("levels add up to %d, not the %d rows asked for", sum, tc.rows)
			}
		})
	}
}

// The curve aims at the level before, and must never reach the rows after
// it: those are the level being inserted, and pointing at them makes the tree
// deeper than asked.
func TestLevelSampleDrawsStayBeforeTheLevel(t *testing.T) {
	level := SelfReferencingLevel{PreviousStart: 100, PreviousEnd: 300}
	s := NewLevelSample(nil, "s", "t", "c", make([][]Getter, 1), level, &ForeignKeyLinks{}).(*LevelSample)

	inPrevious := 0
	const draws = 100000
	for i := 0; i < draws; i++ {
		p := s.draw()
		if p < 0 || p >= level.PreviousEnd {
			t.Fatalf("drew position %d, outside the %d rows there were before this level", p, level.PreviousEnd)
		}
		if p >= level.PreviousStart {
			inPrevious++
		}
	}
	// four standard deviations wide, so about 97.7% land in it once the
	// draws past its end are drawn again
	if share := float64(inPrevious) / draws; share < 0.96 || share > 0.99 {
		t.Errorf("%.3f of the draws landed in the previous level, expected about 0.977", share)
	}
}
