package generate

import (
	"slices"
	"testing"

	"github.com/Percona-Lab/random-data-load/db"
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

// The levels are told apart by a key the database numbers as rows come in,
// and only by one: every key the table has on itself has to point at it.
func TestSelfReferencingKey(t *testing.T) {
	id := db.Field{ColumnName: "id", AutoIncrement: true}
	code := db.Field{ColumnName: "code"}
	other := db.Field{ColumnName: "other_id", AutoIncrement: true}
	selfKey := func(fields ...db.Field) *db.Constraint {
		return &db.Constraint{TableName: "t", ReferencedTableName: "t", ReferencedFields: fields}
	}

	for _, tc := range []struct {
		name        string
		constraints []*db.Constraint
		want        string
	}{
		{name: "an auto-increment key", constraints: []*db.Constraint{selfKey(id)}, want: "id"},
		{name: "two keys on the same column", constraints: []*db.Constraint{selfKey(id), selfKey(id)}, want: "id"},
		{name: "a key the run generates", constraints: []*db.Constraint{selfKey(code)}},
		{name: "a key on two columns", constraints: []*db.Constraint{selfKey(id, code)}},
		{name: "two keys on two columns", constraints: []*db.Constraint{selfKey(id), selfKey(other)}},
		{name: "no key on itself", constraints: []*db.Constraint{{TableName: "t", ReferencedTableName: "p", ReferencedFields: []db.Field{id}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := SelfReferencingKey(&db.Table{Name: "t", Constraints: tc.constraints})
			if got != tc.want || ok != (tc.want != "") {
				t.Errorf("got %q, %v, want %q", got, ok, tc.want)
			}
		})
	}
}

// A key not pointing at the column the levels are told apart by, or a level
// whose bounds were not read, samples without a range rather than an empty one.
func TestLevelKeyRangeLeftOut(t *testing.T) {
	id := []db.Field{{ColumnName: "id"}}
	for _, tc := range []struct {
		name   string
		level  SelfReferencingLevel
		fields []db.Field
	}{
		{name: "no key", level: SelfReferencingLevel{UpTo: "10"}, fields: id},
		{name: "a dry run reads no bound", level: SelfReferencingLevel{Key: "id"}, fields: id},
		{name: "another column", level: SelfReferencingLevel{Key: "other", UpTo: "10"}, fields: id},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.level.keyRange(tc.fields); got != "" {
				t.Errorf("got %q, want no range", got)
			}
		})
	}
}
