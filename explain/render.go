package explain

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// RowsPerTableFlag renders the table sizes as the --rows-per-table the run
// takes. Tables the plan says nothing about are left out rather than guessed.
func (s *Stats) RowsPerTableFlag() string {
	parts := []string{}
	for _, t := range s.Tables {
		if t.Rows > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", t.Table, t.Rows))
		}
	}
	return strings.Join(parts, ";")
}

// ValuesFreqMapFlag renders the predicates as the --values-freq-map the run
// takes, so a column actually holds the values the query looks for, in the
// proportion the reported side held them.
func (s *Stats) ValuesFreqMapFlag() string {
	byColumn := map[string][]string{}
	order := []string{}

	for _, sel := range s.Selectivities {
		if sel.Fraction <= 0 || sel.Fraction > 1 {
			continue
		}
		key := sel.Table + "." + sel.Column
		if _, seen := byColumn[key]; !seen {
			order = append(order, key)
		}
		byColumn[key] = append(byColumn[key], fmt.Sprintf("%s:%s", sel.Value, formatFraction(sel.Fraction)))
	}

	sort.Strings(order)
	parts := []string{}
	for _, key := range order {
		parts = append(parts, key+"="+strings.Join(byColumn[key], ","))
	}
	return strings.Join(parts, ";")
}

// Report writes what the plan gave up, and what it did not.
func (s *Stats) Report() string {
	b := &strings.Builder{}

	fmt.Fprintln(b, "Tables")
	if len(s.Tables) == 0 {
		fmt.Fprintln(b, "  none: no node in this plan reads a table")
	}
	for _, t := range s.Tables {
		fmt.Fprintf(b, "  %-24s %10s rows %8s pages %5s bytes/row", t.Table,
			count(t.Rows), count(t.Pages), count(t.Width))
		if t.Source != "" {
			fmt.Fprintf(b, "   (%s)", t.Source)
		}
		fmt.Fprintln(b)
		if t.Note != "" {
			fmt.Fprintf(b, "  %-24s   %s\n", "", t.Note)
		}
	}

	fmt.Fprintln(b, "\nPredicate selectivities")
	if len(s.Selectivities) == 0 {
		fmt.Fprintln(b, "  none derivable")
	}
	for _, sel := range s.Selectivities {
		fmt.Fprintf(b, "  %-32s = %-24s %8s   (%s)\n",
			sel.Table+"."+sel.Column, sel.Value, formatFraction(sel.Fraction), sel.Source)
	}

	fmt.Fprintln(b, "\nDistinct values")
	if len(s.Distincts) == 0 {
		fmt.Fprintln(b, "  none derivable: only a single-column GROUP BY gives one away")
	}
	for _, d := range s.Distincts {
		fmt.Fprintf(b, "  %-32s %8s   (%s)\n", d.Table+"."+d.Column, count(d.Count), d.Source)
	}

	fmt.Fprintln(b, "\nFan-out")
	if len(s.FanOuts) == 0 {
		fmt.Fprintln(b, "  none derivable: no table is read once per row of another")
	}
	for _, f := range s.FanOuts {
		fmt.Fprintf(b, "  %-32s %8.2f rows per outer row   (%s)\n", f.Child, f.Rows, f.Source)
	}

	if len(s.Warnings) > 0 {
		fmt.Fprintln(b, "\nNotes")
		for _, w := range s.Warnings {
			fmt.Fprintf(b, "  %s\n", w)
		}
	}

	fmt.Fprintln(b, "\nFlags for the run")
	if flag := s.RowsPerTableFlag(); flag != "" {
		fmt.Fprintf(b, "  --rows-per-table=%q\n", flag)
	}
	if flag := s.ValuesFreqMapFlag(); flag != "" {
		fmt.Fprintf(b, "  --values-freq-map=%q\n", flag)
	}
	fmt.Fprintln(b, "\nPage counts assume the default seq_page_cost, cpu_tuple_cost and")
	fmt.Fprintln(b, "cpu_operator_cost. Check the reported side's settings before trusting them.")
	return b.String()
}

// formatFraction keeps a selectivity readable without rounding a rare value
// away to zero.
func formatFraction(f float64) string {
	switch {
	case f >= 0.01:
		return strconv.FormatFloat(f, 'f', 4, 64)
	case f >= 0.0001:
		return strconv.FormatFloat(f, 'f', 6, 64)
	default:
		return strconv.FormatFloat(f, 'g', 3, 64)
	}
}

func count(v int64) string {
	if v == 0 {
		return "?"
	}
	return strconv.FormatInt(v, 10)
}
