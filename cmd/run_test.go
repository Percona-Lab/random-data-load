package cmd

import (
	"testing"

	"github.com/Percona-Lab/random-data-load/db"
	"github.com/Percona-Lab/random-data-load/frequency"
	"github.com/Percona-Lab/random-data-load/generate"
	"github.com/rs/zerolog"
)

func TestMain(m *testing.M) {
	zerolog.SetGlobalLevel(zerolog.Disabled)
	m.Run()
}

func table(schema, name string, columns ...string) *db.Table {
	t := &db.Table{Schema: schema, Name: name}
	for _, column := range columns {
		t.Fields = append(t.Fields, db.Field{ColumnName: column})
	}
	return t
}

func column(table, name string, avgWidth int64, nullFrac float64) frequency.ColumnStats {
	return frequency.ColumnStats{
		Schemaname: "public", Tablename: table, Attname: name,
		AvgWidth: avgWidth, NullFrac: nullFrac,
	}
}

func TestWidthsFromStats(t *testing.T) {
	tests := []struct {
		name  string
		cmd   RunCmd
		stats []frequency.ColumnStats
		table *db.Table
		want  int64 // 0 means no width is to be taken from the dump
	}{
		{
			name:  "every column of the table measured, so the row is the sum of them",
			stats: []frequency.ColumnStats{column("t1", "c1", 4, 0), column("t1", "c2", 40, 0)},
			table: table("public", "t1", "c1", "c2"),
			want:  44,
		},
		{
			// the figure the whole thing turns on: a column NULL on half its
			// rows carries half its width into the row
			name:  "a null fraction takes the column's width out of the rows it is missing from",
			stats: []frequency.ColumnStats{column("t1", "c1", 4, 0), column("t1", "c2", 100, 0.5)},
			table: table("public", "t1", "c1", "c2"),
			want:  54,
		},
		{
			// the case a --query-narrowed dump always produces. Half a row is
			// not a row, and aiming at it would shrink the table
			name:  "a column of the table the dump does not cover leaves the width alone",
			stats: []frequency.ColumnStats{column("t1", "c1", 4, 0)},
			table: table("public", "t1", "c1", "c2"),
			want:  0,
		},
		{
			// the other direction is fine: the source row was that wide, and
			// reaching that width here is the point
			name:  "a column the local table does not have still counts towards the row",
			stats: []frequency.ColumnStats{column("t1", "c1", 4, 0), column("t1", "dropped", 20, 0)},
			table: table("public", "t1", "c1"),
			want:  24,
		},
		{
			name:  "a dump taken before avg_width was exported says nothing about the width",
			stats: []frequency.ColumnStats{column("t1", "c1", 0, 0.25)},
			table: table("public", "t1", "c1"),
			want:  0,
		},
		{
			name:  "a table the dump never mentions",
			stats: []frequency.ColumnStats{column("t2", "c1", 4, 0)},
			table: table("public", "t1", "c1"),
			want:  0,
		},
		{
			// pg_stats holds folded names, a catalog may not
			name:  "names are matched the way the rest of the dump is, case aside",
			stats: []frequency.ColumnStats{column("t1", "c1", 4, 0)},
			table: table("PUBLIC", "T1", "C1"),
			want:  4,
		},
		{
			name:  "a table of another schema is a different table",
			stats: []frequency.ColumnStats{column("t1", "c1", 4, 0)},
			table: table("reporting", "t1", "c1"),
			want:  0,
		},
		{
			name:  "--target-bytes-per-row was asked for by hand, so it wins",
			cmd:   RunCmd{TargetBytesPerRow: generate.PerTableFloat{"t1": 180}},
			stats: []frequency.ColumnStats{column("t1", "c1", 4, 0)},
			table: table("public", "t1", "c1"),
			want:  0,
		},
		{
			name:  "and so does the page count, which is the same target from the other side",
			cmd:   RunCmd{TargetRelpages: generate.PerTableFloat{"t1": 400}},
			stats: []frequency.ColumnStats{column("t1", "c1", 4, 0)},
			table: table("public", "t1", "c1"),
			want:  0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := test.cmd
			cmd.widthsFromStats(test.stats, []*db.Table{test.table})

			if got := cmd.statBytesPerRow["t1"]; got != test.want {
				t.Errorf("the dump put %s at %d bytes per row, want %d", test.table.Name, got, test.want)
			}
		})
	}
}

// The dump is the last of the three ways of asking for a row width, so a flag
// still decides whatever the dump measured.
func TestRowWidthTargetPrefersTheFlags(t *testing.T) {
	t1 := table("public", "t1", "c1")

	cmd := RunCmd{statBytesPerRow: map[string]int64{"t1": 44}}
	if got := cmd.rowWidthTarget(t1, 20000); got != 44 {
		t.Errorf("with nothing but a dump, the target is %d, want 44", got)
	}

	cmd.TargetBytesPerRow = generate.PerTableFloat{"t1": 180}
	if got := cmd.rowWidthTarget(t1, 20000); got != 180 {
		t.Errorf("--target-bytes-per-row gave %d, want 180", got)
	}

	// 20,000 rows over 400 pages, which the page layout turns back into a row
	// width of its own, and never into the dump's 44
	cmd = RunCmd{statBytesPerRow: map[string]int64{"t1": 44}, TargetRelpages: generate.PerTableFloat{"t1": 400}}
	if got := cmd.rowWidthTarget(t1, 20000); got == 44 || got <= 0 {
		t.Errorf("--target-relpages gave %d, want a width worked out from the page count", got)
	}

	cmd = RunCmd{}
	if got := cmd.rowWidthTarget(t1, 20000); got != 0 {
		t.Errorf("a run given no width at all aims at %d, want 0", got)
	}
}
