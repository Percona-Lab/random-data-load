package cmd

import (
	"strings"
	"testing"

	"github.com/Percona-Lab/random-data-load/db"
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
	report := &verifyReport{tolerance: 0.05, all: true}
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

// Without --all the report is what needs looking at: the figures off their
// target and the ones that could not be read back. A case-2 verify was 7 KB of
// which the three OFF lines were 0.4; everything else is counted, so a short
// report still says how much it checked.
func TestReportByDefaultShowsOnlyWhatNeedsLooking(t *testing.T) {
	report := &verifyReport{tolerance: 0.05}
	report.add(comparison{subject: "public.orders", figure: figureRows, reported: 500000, hasTarget: true, generated: 500000, measured: true, source: "given"})
	report.add(comparison{subject: "public.inventory", figure: figureRows, reported: 400000, hasTarget: true, generated: 0, measured: true, source: "given"})
	report.add(comparison{subject: "public.addresses", figure: figurePages, generated: 2634, measured: true, note: "nothing has analyzed this table"})
	report.add(comparison{subject: "public.orders", figure: figureWidth, reported: 16, hasTarget: true, generated: 149, measured: true, advisory: true, unlike: true})
	// a stale ANALYZE is advisory, and still worth seeing when it is off
	report.add(comparison{subject: "public.orders", figure: figureTuples, reported: 500000, hasTarget: true, generated: 100, measured: true, advisory: true})
	report.add(comparison{subject: "public.orders.status = cancelled", figure: figureSelectivity, reported: 0.04, hasTarget: true, measured: false})
	report.add(comparison{subject: "public.orders.is_gift = f", figure: figureValueFreq, reported: 0.9, hasTarget: true, generated: 0.9, measured: true, source: "--stat-file"})

	rendered := report.render()
	for _, want := range []string{
		"public.inventory", "OFF",
		"Estimated rows", "advisory",
		"public.orders.status = cancelled", "not measured",
		"Not shown: 2 within 0.0500 of their target, 1 with no target, 1 whose two sides measure different things. --all prints every figure.",
		"1 figure sits outside",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the report does not mention %q:\n%s", want, rendered)
		}
	}
	for _, unwanted := range []string{"public.addresses", "Bytes/row", "Value frequency", "is_gift", "   no target\n", "nothing has analyzed"} {
		if strings.Contains(rendered, unwanted) {
			t.Errorf("the report prints %q, which needs no looking at:\n%s", unwanted, rendered)
		}
	}
	if report.off != 1 {
		t.Errorf("off = %d, want 1: hiding a line must not change what --strict fails on", report.off)
	}
}

// A caption every line of a section carries goes on the section, once.
func TestReportSaysASharedCaptionOnce(t *testing.T) {
	report := &verifyReport{tolerance: 0.05, all: true}
	for _, table := range []string{"public.a", "public.b", "public.c"} {
		report.add(comparison{subject: table, figure: figureTuples, reported: 100, hasTarget: true, generated: 100, measured: true, advisory: true,
			source: "counted rows vs what the planner believes the table holds"})
	}
	report.add(comparison{subject: "public.a", figure: figurePages, generated: 1, measured: true, note: "nothing has analyzed this table"})
	report.add(comparison{subject: "public.b", figure: figurePages, generated: 1, measured: true})

	rendered := report.render()
	if n := strings.Count(rendered, "counted rows vs what the planner believes"); n != 1 {
		t.Errorf("the shared caption is printed %d times, want once:\n%s", n, rendered)
	}
	if !strings.Contains(rendered, "Estimated rows  (counted rows vs what the planner believes the table holds)") {
		t.Errorf("the shared caption is not on the section:\n%s", rendered)
	}
	// a detail only some lines carry stays under the line it belongs to
	if !strings.Contains(rendered, "\n  "+strings.Repeat(" ", 44)+"   nothing has analyzed this table\n") {
		t.Errorf("a detail one line carries left that line:\n%s", rendered)
	}
	if strings.Contains(rendered, "Pages  (") {
		t.Errorf("a section with a line carrying no detail got a caption:\n%s", rendered)
	}
}

// Where every line carries a detail but not all the same, the one most carry
// goes on the section and the others stay under their own line.
func TestReportSaysTheMostCommonCaptionOnce(t *testing.T) {
	report := &verifyReport{tolerance: 0.05, all: true}
	for _, value := range []string{"a", "b", "c"} {
		report.add(comparison{subject: "public.t.c = " + value, figure: figureValueFreq, reported: 0.1, hasTarget: true, generated: 0.1, measured: true, source: "--stat-file"})
	}
	report.add(comparison{subject: "public.t.fk = 1", figure: figureValueFreq, reported: 0.1, hasTarget: true, generated: 0, measured: true, advisory: true, unlike: true,
		source: "--stat-file: a foreign key holds this run's parent ids, not the source's"})

	rendered := report.render()
	if !strings.Contains(rendered, "Value frequency  (--stat-file)\n") {
		t.Errorf("the caption most lines carry is not on the section:\n%s", rendered)
	}
	if n := strings.Count(rendered, "--stat-file"); n != 2 {
		t.Errorf("--stat-file is said %d times, want twice: once on the section, once for the key:\n%s", n, rendered)
	}
}

// A dump's values for a key are the source's parent ids, which run does not
// insert, so verify has to know a key when it sees one, spelled however the
// dump spells it.
func TestIsForeignKey(t *testing.T) {
	orders := table("public", "orders", "id", "customer_id", "status")
	orders.Constraints = []*db.Constraint{{ConstraintName: "orders_customer_fk", ColumnsName: []string{"customer_id"}, ReferencedTableName: "customers"}}

	for column, want := range map[string]bool{"customer_id": true, "CUSTOMER_ID": true, "status": false, "id": false, "missing": false} {
		if got := isForeignKey(orders, column); got != want {
			t.Errorf("isForeignKey(orders, %q) = %v, want %v", column, got, want)
		}
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
