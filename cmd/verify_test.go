package cmd

import (
	"strings"
	"testing"

	"github.com/Percona-Lab/random-data-load/frequency"
)

func TestComparisonVerdict(t *testing.T) {
	tests := []struct {
		name      string
		c         comparison
		tolerance float64
		want      string
	}{
		{
			name: "a figure the reported side never gave is not a miss",
			c:    comparison{figure: figurePages, generated: 412, measured: true},
			want: "no target",
		},
		{
			// the case a bare "is it zero" test gets wrong: postgres saw no
			// NULL in the column, and the run has to produce none either
			name:      "a null fraction of zero is a target",
			c:         comparison{figure: figureNullFrac, reported: 0, hasTarget: true, generated: 0.4, measured: true},
			tolerance: 0.05,
			want:      "OFF",
		},
		{
			name:      "and it is met by nothing at all",
			c:         comparison{figure: figureNullFrac, reported: 0, hasTarget: true, generated: 0, measured: true},
			tolerance: 0.05,
			want:      "ok",
		},
		{
			name:      "a row count asked for as zero is not met by rows",
			c:         comparison{figure: figureRows, reported: 0, hasTarget: true, generated: 12, measured: true},
			tolerance: 0.05,
			want:      "OFF",
		},
		{
			// 500 rows out of 500,000 is nothing, out of 500 it is everything,
			// which is why the comparison is relative
			name:      "a row count within the tolerance",
			c:         comparison{figure: figureRows, reported: 500000, hasTarget: true, generated: 500500, measured: true},
			tolerance: 0.05,
			want:      "ok",
		},
		{
			name:      "the same absolute miss on a small table",
			c:         comparison{figure: figureRows, reported: 500, hasTarget: true, generated: 1000, measured: true},
			tolerance: 0.05,
			want:      "OFF",
		},
		{
			name:      "an advisory line never fails",
			c:         comparison{figure: figureWidth, reported: 21, hasTarget: true, generated: 97, measured: true, advisory: true},
			tolerance: 0.05,
			want:      "advisory",
		},
		{
			name:      "a figure the engine refused to measure",
			c:         comparison{figure: figureSelectivity, reported: 0.4, hasTarget: true, measured: false},
			tolerance: 0.05,
			want:      "not measured",
		},
	}

	for _, test := range tests {
		if got := test.c.verdict(test.tolerance); got != test.want {
			t.Errorf("%s: verdict = %q, want %q", test.name, got, test.want)
		}
	}
}

// Only a line with a target, measured and not advisory, can put the report out.
func TestReportCountsOnlyRealMisses(t *testing.T) {
	report := &verifyReport{tolerance: 0.05}
	report.add(comparison{subject: "t1", figure: figureRows, reported: 100, hasTarget: true, generated: 100, measured: true})
	report.add(comparison{subject: "t1", figure: figurePages, generated: 412, measured: true})
	report.add(comparison{subject: "t1", figure: figureWidth, reported: 21, hasTarget: true, generated: 97, measured: true, advisory: true})
	report.add(comparison{subject: "t1.c1 = 42", figure: figureValueFreq, reported: 0.6, hasTarget: true, generated: 0.3, measured: true})

	if report.off != 1 {
		t.Fatalf("off = %d, want 1", report.off)
	}

	rendered := report.render()
	for _, want := range []string{"Rows", "Pages", "Value frequency", "t1.c1 = 42", "1 figure sits outside"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the report does not mention %q:\n%s", want, rendered)
		}
	}
	// a figure that was never asked for has nothing to be off by
	if strings.Contains(rendered, "OFF") != true {
		t.Errorf("the missed value frequency is not called out:\n%s", rendered)
	}
}

func TestReportSaysSoWhenNothingIsOff(t *testing.T) {
	report := &verifyReport{tolerance: 0.05}
	report.add(comparison{subject: "t1", figure: figureRows, reported: 100, hasTarget: true, generated: 102, measured: true})

	if got := report.summary(); !strings.HasPrefix(got, "Every figure") {
		t.Errorf("summary = %q", got)
	}
}

// The export's avg_width added up is the same sum the catalog reports back for
// the generated table, so it is the row width to hold a run to -- when the
// export covers the whole row.
func TestWidthFromStats(t *testing.T) {
	tests := []struct {
		name    string
		target  verifyTarget
		want    int64
		wantWhy string // substring the explanation has to carry
	}{
		{
			name: "every column of the table measured",
			target: verifyTarget{
				table:   table("public", "t1", "c1", "c2"),
				columns: []frequency.ColumnStats{column("t1", "c1", 4, 0), column("t1", "c2", 60, 0.5)},
			},
			// null_frac is not in it: an avg_width is measured over the rows
			// holding a value, and so is the one it is compared against
			want: 64,
		},
		{
			name: "a column of the table the export does not cover",
			target: verifyTarget{
				table:   table("public", "t1", "c1", "c2"),
				columns: []frequency.ColumnStats{column("t1", "c1", 4, 0)},
			},
			wantWhy: "c2",
		},
		{
			name: "an export taken before avg_width was in it",
			target: verifyTarget{
				table:   table("public", "t1", "c1"),
				columns: []frequency.ColumnStats{column("t1", "c1", 0, 0.25)},
			},
		},
		{
			name:   "no export at all",
			target: verifyTarget{table: table("public", "t1", "c1")},
		},
		{
			name: "a table that could not be read back says nothing about its width",
			target: verifyTarget{
				table:      table("public", "t1", "c1"),
				columns:    []frequency.ColumnStats{column("t1", "c1", 4, 0)},
				unreadable: "no such table",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, why := test.target.widthFromStats()

			if got != test.want {
				t.Errorf("widthFromStats() = %d, want %d", got, test.want)
			}
			switch {
			case test.wantWhy == "" && why != "":
				t.Errorf("widthFromStats() explained itself with %q, there was nothing to explain", why)
			case test.wantWhy != "" && !strings.Contains(why, test.wantWhy):
				t.Errorf("widthFromStats() explained itself with %q, which never names %q", why, test.wantWhy)
			}
		})
	}
}
