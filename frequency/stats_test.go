package frequency

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestMain(m *testing.M) {
	zerolog.SetGlobalLevel(zerolog.Disabled)
	m.Run()
}

// sameTable resolves every dumped column onto itself, which is what a run whose
// destination table carries the source's name and columns ends up doing.
func sameTable(cs ColumnStats) (string, string, bool) { return cs.Tablename, cs.Attname, true }

func TestParseStats(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []ColumnStats
		wantErr bool
	}{
		{
			name:  "a row of every field",
			input: `[{"schemaname":"public","tablename":"t1","attname":"c1","null_frac":0.25,"most_common_vals":["a","b"],"most_common_freqs":[0.5,0.2]}]`,
			want: []ColumnStats{{
				Schemaname: "public", Tablename: "t1", Attname: "c1", NullFrac: 0.25,
				MostCommonVals: []string{"a", "b"}, MostCommonFreqs: []float64{0.5, 0.2},
			}},
		},
		{
			name:  "a column with no statistics at all still carries its null_frac",
			input: `[{"schemaname":"public","tablename":"t1","attname":"c3","null_frac":0,"most_common_vals":null,"most_common_freqs":null}]`,
			want:  []ColumnStats{{Schemaname: "public", Tablename: "t1", Attname: "c3"}},
		},
		{
			name:  "values holding the separators a csv dump would have needed escaped",
			input: `[{"tablename":"t1","attname":"c1","most_common_vals":["it's, quoted","two\nlines"],"most_common_freqs":[0.5,0.2]}]`,
			want: []ColumnStats{{
				Tablename: "t1", Attname: "c1",
				MostCommonVals: []string{"it's, quoted", "two\nlines"}, MostCommonFreqs: []float64{0.5, 0.2},
			}},
		},
		{name: "an empty array", input: `[]`, want: []ColumnStats{}},
		{name: "psql printing nothing for an empty result", input: "\n", want: []ColumnStats{}},
		{name: "psql printing a null aggregate", input: `\N`, want: []ColumnStats{}},
		{name: "not json at all", input: `most_common_vals`, wantErr: true},
		{name: "a json object rather than the expected array", input: `{"tablename":"t1"}`, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseStats(strings.NewReader(test.input))
			if test.wantErr {
				if err == nil {
					t.Fatalf("ParseStats(%q) = %v, want an error", test.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseStats(%q): %v", test.input, err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("ParseStats(%q) =\n\t%+v\nwant\n\t%+v", test.input, got, test.want)
			}
		})
	}
}

func TestMergeStats(t *testing.T) {
	tests := []struct {
		name string
		// existing is what the command line flags left behind before the dump
		// is read.
		existing  ColumnFrequency
		stats     []ColumnStats
		resolve   Resolver
		wantVals  []string
		wantFreqs []float64
		wantNull  float64
	}{
		{
			name:      "most common values keep the frequency postgres measured",
			stats:     []ColumnStats{{Tablename: "t1", Attname: "c1", MostCommonVals: []string{"a", "b"}, MostCommonFreqs: []float64{0.5, 0.2}}},
			wantVals:  []string{"a", "b"},
			wantFreqs: []float64{0.5, 0.2},
		},
		{
			name: "null_frac is scaled up by the share the values already took",
			// an index value is drawn after the null and overwrites it, so 0.1
			// of the whole column is 0.1/(1-0.6) of what is left to it
			stats:    []ColumnStats{{Tablename: "t1", Attname: "c1", NullFrac: 0.1, MostCommonVals: []string{"a"}, MostCommonFreqs: []float64{0.6}}},
			wantVals: []string{"a"}, wantFreqs: []float64{0.6},
			wantNull: 0.25,
		},
		{
			name:     "null_frac is taken as-is when no value competes with it",
			stats:    []ColumnStats{{Tablename: "t1", Attname: "c1", NullFrac: 0.42}},
			wantNull: 0.42,
		},
		{
			name:     "a column postgres saw no null in loses the --null-freq default",
			existing: ColumnFrequency{"c1": {Null: 0.9}},
			stats:    []ColumnStats{{Tablename: "t1", Attname: "c1", NullFrac: 0}},
			wantNull: 0,
		},
		{
			name:     "--null-freq-map wins over the dump",
			existing: ColumnFrequency{"c1": {Null: 0.3, nullFromFlag: true}},
			stats:    []ColumnStats{{Tablename: "t1", Attname: "c1", NullFrac: 0.9}},
			wantNull: 0.3,
		},
		{
			name:      "a value already carrying a frequency is not given a second one",
			existing:  ColumnFrequency{"c1": {IndexValues: []string{"a"}, IndexFrequencies: []float64{0.8}}},
			stats:     []ColumnStats{{Tablename: "t1", Attname: "c1", MostCommonVals: []string{"a", "b"}, MostCommonFreqs: []float64{0.5, 0.2}}},
			wantVals:  []string{"a", "b"},
			wantFreqs: []float64{0.8, 0.2},
		},
		{
			name:      "a value left at a frequency of zero claims nothing",
			existing:  ColumnFrequency{"c1": {IndexValues: []string{"a"}, IndexFrequencies: []float64{0}}},
			stats:     []ColumnStats{{Tablename: "t1", Attname: "c1", MostCommonVals: []string{"a"}, MostCommonFreqs: []float64{0.5}}},
			wantVals:  []string{"a", "a"},
			wantFreqs: []float64{0, 0.5},
		},
		{
			name:      "a value postgres saw on no row is dropped",
			stats:     []ColumnStats{{Tablename: "t1", Attname: "c1", MostCommonVals: []string{"a", "b"}, MostCommonFreqs: []float64{0, 0.2}}},
			wantVals:  []string{"b"},
			wantFreqs: []float64{0.2},
		},
		{
			name:      "arrays of different lengths fall back to the shorter one",
			stats:     []ColumnStats{{Tablename: "t1", Attname: "c1", MostCommonVals: []string{"a", "b", "c"}, MostCommonFreqs: []float64{0.5}}},
			wantVals:  []string{"a"},
			wantFreqs: []float64{0.5},
		},
		{
			name:      "values covering every row leave no room for a null",
			existing:  ColumnFrequency{"c1": {IndexValues: []string{"a"}, IndexFrequencies: []float64{1}}},
			stats:     []ColumnStats{{Tablename: "t1", Attname: "c1", NullFrac: 0.5}},
			wantVals:  []string{"a"},
			wantFreqs: []float64{1},
			wantNull:  0,
		},
		{
			// --query-param-freq stacks its literal on top of the dump, so the
			// share left for the nulls can end up smaller than null_frac asks
			// for. They then take everything that is left, and no more.
			name:      "a null_frac the remaining rows cannot hold is capped",
			existing:  ColumnFrequency{"c1": {IndexValues: []string{"a"}, IndexFrequencies: []float64{0.8}}},
			stats:     []ColumnStats{{Tablename: "t1", Attname: "c1", NullFrac: 0.4}},
			wantVals:  []string{"a"},
			wantFreqs: []float64{0.8},
			wantNull:  1,
		},
		{
			name:     "a column the run does not insert into is skipped",
			stats:    []ColumnStats{{Tablename: "other", Attname: "c1", NullFrac: 0.9, MostCommonVals: []string{"a"}, MostCommonFreqs: []float64{0.5}}},
			resolve:  func(cs ColumnStats) (string, string, bool) { return "", "", false },
			wantNull: 0,
		},
		{
			// pg_stats reports folded names while the catalog may hold another
			// spelling, so the caller settles the naming
			name:  "the resolver decides which table and column the dump lands on",
			stats: []ColumnStats{{Tablename: "T1", Attname: "C1", NullFrac: 0.4}},
			resolve: func(cs ColumnStats) (string, string, bool) {
				return strings.ToLower(cs.Tablename), strings.ToLower(cs.Attname), true
			},
			wantNull: 0.4,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			SharedTableFrequency = map[string]ColumnFrequency{}
			if test.existing != nil {
				SharedTableFrequency["t1"] = test.existing
			}
			resolve := test.resolve
			if resolve == nil {
				resolve = sameTable
			}

			MergeStats(test.stats, resolve)

			got := SharedTableFrequency["t1"]["c1"]
			if !reflect.DeepEqual(nonEmpty(got.IndexValues), nonEmpty(test.wantVals)) {
				t.Errorf("values = %q, want %q", got.IndexValues, test.wantVals)
			}
			if !reflect.DeepEqual(nonEmpty(got.IndexFrequencies), nonEmpty(test.wantFreqs)) {
				t.Errorf("frequencies = %v, want %v", got.IndexFrequencies, test.wantFreqs)
			}
			if math.Abs(got.Null-test.wantNull) > 1e-9 {
				t.Errorf("null = %v, want %v", got.Null, test.wantNull)
			}
		})
	}
}

// nonEmpty normalises a nil slice and an empty one, which say the same thing
// here, so that a case expecting no value can just leave the field out.
func nonEmpty[E any](s []E) []E {
	if len(s) == 0 {
		return nil
	}
	return s
}

// TestMergeStatsReproducesTheDump checks the whole point of the feature: the
// frequencies stored have to be the ones that make the generated column come
// out with the null_frac and the value shares that were measured.
func TestMergeStatsReproducesTheDump(t *testing.T) {
	const rows = 400000
	stats := []ColumnStats{{
		Tablename: "t1", Attname: "c1", NullFrac: 0.1,
		MostCommonVals: []string{"a", "b"}, MostCommonFreqs: []float64{0.6, 0.15},
	}}

	SharedTableFrequency = map[string]ColumnFrequency{}
	MergeStats(stats, sameTable)
	freqs := SharedTableFrequency["t1"]

	seen := map[string]int{}
	for i := 0; i < rows; i++ {
		// the same two draws, in the same order, that NewGetterWrapper makes
		value := "other"
		if freqs.Null("c1", true) {
			value = "NULL"
		}
		if injected, ok := freqs.InjectIndexValue("c1"); ok {
			value = injected
		}
		seen[value]++
	}

	for value, want := range map[string]float64{"a": 0.6, "b": 0.15, "NULL": 0.1, "other": 0.15} {
		got := float64(seen[value]) / rows
		if math.Abs(got-want) > 0.005 {
			t.Errorf("%q ended up on %.4f of the rows, want %.4f", value, got, want)
		}
	}
}
