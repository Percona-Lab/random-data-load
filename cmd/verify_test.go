package cmd

import (
	"strings"
	"testing"
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
