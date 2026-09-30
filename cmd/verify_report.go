package cmd

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The figures a run is held against, in the order they are printed: a row
// count is what every share below it is a share of, and a page count is what
// the plan's scan costs were built from.
const (
	figureRows        = "rows"
	figurePages       = "pages"
	figureWidth       = "bytes/row"
	figureTuples      = "estimated rows"
	figureSelectivity = "selectivity"
	figureDistinct    = "distinct values"
	figureNullFrac    = "null fraction"
	figureValueFreq   = "value frequency"
)

var figureOrder = []string{
	figureRows, figurePages, figureWidth, figureTuples,
	figureSelectivity, figureDistinct, figureNullFrac, figureValueFreq,
}

// fractionFigures are the ones written as a share of a table rather than as a
// count, which decides how they are printed and what "close enough" means.
var fractionFigures = map[string]bool{
	figureSelectivity: true,
	figureNullFrac:    true,
	figureValueFreq:   true,
}

// comparison is one figure, as the reported side gave it and as the generated
// tables hold it.
//
// hasTarget is what separates a figure the reported side never gave from one
// it gave as zero, which a bare "is it zero" test cannot: a plan reaching a
// table only through an index says nothing about its size, while a null_frac
// of 0 is a measurement, and a column that came back 2% null has missed it.
// A line without a target still prints what was generated, because "what did
// this actually produce" is half of what the caller came for.
type comparison struct {
	subject   string
	figure    string
	reported  float64
	hasTarget bool
	generated float64
	measured  bool
	note      string
	source    string

	// advisory marks a line worth printing but not worth failing on, because
	// the two sides are not quite measuring the same thing.
	advisory bool

	// unlike marks a line whose two sides are known to measure different
	// things, so that a difference between them is expected and says nothing
	// about the data. It is only printed with --all.
	unlike bool
}

type verifyReport struct {
	tolerance   float64
	comparisons []comparison
	notes       []string
	off         int

	// all prints every line. Without it only the lines that need looking at
	// are, and the rest are counted.
	all bool
}

func (r *verifyReport) add(c comparison) {
	if c.measured && !c.advisory && c.hasTarget && !c.within(r.tolerance) {
		r.off++
	}
	r.comparisons = append(r.comparisons, c)
}

func (r *verifyReport) note(note string) {
	for _, existing := range r.notes {
		if existing == note {
			return
		}
	}
	r.notes = append(r.notes, note)
}

// within reports whether the generated figure is close enough to the reported
// one. The comparison is relative: being 400 rows out means nothing without
// knowing whether the table holds 500 or 500,000.
func (c comparison) within(tolerance float64) bool {
	if !c.hasTarget {
		return true
	}
	// Nothing is a share of zero. A count asked for as zero has to come back
	// as zero, and a fraction asked for as zero is met by anything small
	// enough to round to it.
	if c.reported == 0 {
		return c.generated == 0 ||
			(fractionFigures[c.figure] && c.generated <= tolerance)
	}
	delta := (c.generated - c.reported) / c.reported
	if delta < 0 {
		delta = -delta
	}
	return delta <= tolerance
}

func (c comparison) delta() string {
	if c.reported == 0 || !c.hasTarget || !c.measured {
		return ""
	}
	return fmt.Sprintf("%+.1f%%", 100*(c.generated-c.reported)/c.reported)
}

func (c comparison) verdict(tolerance float64) string {
	switch {
	case !c.measured:
		return "not measured"
	case !c.hasTarget:
		return "no target"
	case c.advisory:
		return "advisory"
	case c.within(tolerance):
		return "ok"
	}
	return "OFF"
}

func (c comparison) format(v float64) string {
	if !fractionFigures[c.figure] {
		return strconv.FormatInt(int64(v), 10)
	}
	return formatFraction(v)
}

// needsLooking is whether a line is printed without --all: a figure off its
// target, or one that could not be read back. A figure within its target, one
// with no target, and one whose sides measure different things are counted
// instead.
func (r *verifyReport) needsLooking(c comparison) bool {
	switch {
	case !c.measured:
		return true
	case !c.hasTarget, c.unlike:
		return false
	}
	return !c.within(r.tolerance)
}

// render writes the reported-versus-generated table, figure by figure.
func (r *verifyReport) render() string {
	b := &strings.Builder{}
	var within, noTarget, unlike int

	for _, figure := range figureOrder {
		lines := []comparison{}
		for _, c := range r.forFigure(figure) {
			switch {
			case r.all || r.needsLooking(c):
				lines = append(lines, c)
			case !c.hasTarget:
				noTarget++
			case c.unlike:
				unlike++
			default:
				within++
			}
		}
		if len(lines) == 0 {
			continue
		}

		// A caption every line of the section shares is said once, on the
		// section, rather than under each of its lines.
		title := strings.ToUpper(figure[:1]) + figure[1:]
		shared := sharedDetail(lines)
		if shared != "" {
			title += "  (" + shared + ")"
		}
		fmt.Fprintf(b, "%s\n", title)
		for _, c := range lines {
			reported := "-"
			if c.hasTarget {
				reported = c.format(c.reported)
			}
			generated := "-"
			if c.measured {
				generated = c.format(c.generated)
			}
			fmt.Fprintf(b, "  %-44s reported %12s   generated %12s %8s   %s\n",
				truncate(c.subject, 44), reported, generated, c.delta(), c.verdict(r.tolerance))
			if detail := c.detail(); detail != "" && detail != shared {
				fmt.Fprintf(b, "  %-44s   %s\n", "", detail)
			}
		}
		fmt.Fprintln(b)
	}

	if len(r.comparisons) == 0 {
		fmt.Fprintln(b, "Nothing could be read back.")
	}
	if hidden := r.hidden(within, noTarget, unlike); hidden != "" {
		fmt.Fprintln(b, hidden)
		fmt.Fprintln(b)
	}

	if len(r.notes) > 0 {
		fmt.Fprintln(b, "Notes")
		for _, note := range r.notes {
			fmt.Fprintf(b, "  %s\n", note)
		}
		fmt.Fprintln(b)
	}

	fmt.Fprintln(b, r.summary())
	return b.String()
}

// summary is the one line a caller reads when it reads nothing else, and the
// message --strict fails with.
func (r *verifyReport) summary() string {
	switch r.off {
	case 0:
		return fmt.Sprintf("Every figure with a target sits within %s of it.", formatFraction(r.tolerance))
	case 1:
		return fmt.Sprintf("1 figure sits outside %s of its target.", formatFraction(r.tolerance))
	}
	return fmt.Sprintf("%d figures sit outside %s of their target.", r.off, formatFraction(r.tolerance))
}

// detail is what the line needs said about it: where the reported figure came
// from, or why the generated one is approximate.
func (c comparison) detail() string {
	parts := []string{}
	if c.source != "" {
		parts = append(parts, c.source)
	}
	if c.note != "" {
		parts = append(parts, c.note)
	}
	return strings.Join(parts, "; ")
}

// hidden is the line saying what was left out without --all, so that a short
// report still says how much it checked.
func (r *verifyReport) hidden(within, noTarget, unlike int) string {
	parts := []string{}
	if within > 0 {
		parts = append(parts, fmt.Sprintf("%d within %s of their target", within, formatFraction(r.tolerance)))
	}
	if noTarget > 0 {
		parts = append(parts, fmt.Sprintf("%d with no target", noTarget))
	}
	if unlike > 0 {
		parts = append(parts, fmt.Sprintf("%d whose two sides measure different things", unlike))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Not shown: " + strings.Join(parts, ", ") + ". --all prints every figure."
}

// sharedDetail is the detail said once on the section: the one most of its
// lines carry, when every line carries one. A line carrying another still
// says its own. When some line carries none, nothing goes on the section,
// which would otherwise read as if that line carried it too.
func sharedDetail(lines []comparison) string {
	count := map[string]int{}
	shared := ""
	for _, c := range lines {
		detail := c.detail()
		if detail == "" {
			return ""
		}
		count[detail]++
		if count[detail] > count[shared] || (count[detail] == count[shared] && detail < shared) {
			shared = detail
		}
	}
	if count[shared] < 2 && len(lines) > 1 {
		return ""
	}
	return shared
}

func (r *verifyReport) forFigure(figure string) []comparison {
	lines := []comparison{}
	for _, c := range r.comparisons {
		if c.figure == figure {
			lines = append(lines, c)
		}
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].subject < lines[j].subject })
	return lines
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
