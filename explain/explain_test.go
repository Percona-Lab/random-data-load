package explain

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func load(t *testing.T, name string) *Plan {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading the plan: %v", err)
	}
	plan := Parse(string(text))
	if len(plan.Nodes) == 0 {
		t.Fatalf("%s parsed to no nodes", name)
	}
	return plan
}

func TestParseNames(t *testing.T) {
	tests := []struct {
		desc                         string
		name, relation, alias, index string
	}{
		{desc: "Seq Scan on customers c", name: "Seq Scan", relation: "customers", alias: "c"},
		{desc: "Seq Scan on customers", name: "Seq Scan", relation: "customers", alias: "customers"},
		{desc: "Parallel Bitmap Heap Scan on orders o", name: "Parallel Bitmap Heap Scan", relation: "orders", alias: "o"},
		// a bitmap index scan names its index where every other node names a table
		{desc: "Bitmap Index Scan on orders_status_idx", name: "Bitmap Index Scan", index: "orders_status_idx"},
		{desc: "Index Scan using products_pkey on products p", name: "Index Scan", relation: "products", alias: "p", index: "products_pkey"},
		{desc: "Index Only Scan using i on t", name: "Index Only Scan", relation: "t", alias: "t", index: "i"},
		{desc: "Seq Scan on public.orders o", name: "Seq Scan", relation: "orders", alias: "o"},
		{desc: "Nested Loop", name: "Nested Loop"},
		{desc: "Finalize GroupAggregate", name: "Finalize GroupAggregate"},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			name, relation, alias, index := parseDescriptor(test.desc)
			if name != test.name || relation != test.relation || alias != test.alias || index != test.index {
				t.Errorf("parseDescriptor(%q) = (%q, %q, %q, %q), want (%q, %q, %q, %q)",
					test.desc, name, relation, alias, index, test.name, test.relation, test.alias, test.index)
			}
		})
	}
}

// The figures here are the ones the study worked out by hand from this plan,
// which is the whole point of the command: 45 pages behind a cost of 107.50,
// and an enterprise share of 5.28%.
func TestDeriveNestedLoop(t *testing.T) {
	stats := load(t, "nested_loop.txt").Derive(map[string]int64{"orders": 120000, "order_items": 396503})

	assertTable(t, stats, "customers", 5000, 45)
	assertSelectivity(t, stats, "customers", "segment", "enterprise", 0.0528)
	assertSelectivity(t, stats, "orders", "status", "cancelled", 0.0406)
	assertDistinct(t, stats, "customers", "country", 8)
	assertFanOut(t, stats, "order_items", 3)
}

// A parallel plan splits its counts across workers, and the same predicate is
// written on three nodes that do not agree. The bitmap index scan is the one
// that counted every matching row.
func TestDeriveParallel(t *testing.T) {
	stats := load(t, "parallel.txt").Derive(map[string]int64{
		"orders": 500000, "customers": 200000, "order_items": 1504919,
		"products": 300000, "categories": 100000, "suppliers": 100000,
	})

	assertSelectivity(t, stats, "orders", "status", "cancelled", 0.0403)
	assertSelectivity(t, stats, "customers", "segment", "enterprise", 0.0501)
	assertFanOut(t, stats, "order_items", 3)

	// a two-column GROUP BY bounds the product rather than either column
	if len(stats.Warnings) == 0 {
		t.Error("a multi-column group key should still be reported")
	}
}

// Joining a CTE to a derived table changes none of this: the predicates still
// belong to the tables underneath.
func TestDeriveThroughCTEAndSubquery(t *testing.T) {
	stats := load(t, "cte_and_subquery.txt").Derive(map[string]int64{
		"orders": 500000, "customers": 200000, "order_items": 1504919,
		"products": 300000, "suppliers": 100000,
	})

	assertSelectivity(t, stats, "orders", "status", "cancelled", 0.0403)
	assertSelectivity(t, stats, "customers", "segment", "enterprise", 0.0501)
	assertDistinct(t, stats, "suppliers", "country", 8)
}

// Without the reported sizes a table reached only through an index has none,
// and the report has to say so rather than invent one.
func TestDeriveWithoutGivenSizes(t *testing.T) {
	stats := load(t, "nested_loop.txt").Derive(nil)

	assertTable(t, stats, "customers", 5000, 45)
	for _, table := range stats.Tables {
		if table.Table == "order_items" {
			if table.Rows != 0 {
				t.Errorf("order_items is only ever indexed, so its size cannot be known, got %d", table.Rows)
			}
			if table.Note == "" {
				t.Error("a table with no derivable size has to say why")
			}
		}
	}
	if len(stats.Warnings) == 0 {
		t.Error("deriving without any given size should warn that selectivities will be missing")
	}
}

func TestFlagsAreRunnable(t *testing.T) {
	stats := load(t, "nested_loop.txt").Derive(map[string]int64{"orders": 120000})

	if got, want := stats.RowsFlag(), "customers=5000;orders=120000"; got != want {
		t.Errorf("RowsFlag() = %q, want %q", got, want)
	}
	want := "customers.segment=enterprise:0.0528;orders.status=cancelled:0.0406"
	if got := stats.ValuesFreqMapFlag(); got != want {
		t.Errorf("ValuesFreqMapFlag() = %q, want %q", got, want)
	}
}

func TestParsePredicates(t *testing.T) {
	tests := []struct {
		cond   string
		column string
		value  string
	}{
		{cond: "((segment)::text = 'enterprise'::text)", column: "segment", value: "enterprise"},
		{cond: "((c.segment)::text = 'enterprise'::text)", column: "segment", value: "enterprise"},
		{cond: "(status = 'x'::text)", column: "status", value: "x"},
		{cond: "(customer_id = 5)", column: "customer_id", value: "5"},
		{cond: "(NOT is_discontinued)", column: "is_discontinued", value: "false"},
		{cond: "is_gift", column: "is_gift", value: "true"},
	}

	for _, test := range tests {
		t.Run(test.cond, func(t *testing.T) {
			preds := parsePredicates(test.cond)
			if len(preds) == 0 {
				t.Fatalf("parsePredicates(%q) found nothing", test.cond)
			}
			if preds[0].column != test.column || preds[0].value != test.value {
				t.Errorf("parsePredicates(%q) = %q=%q, want %q=%q",
					test.cond, preds[0].column, preds[0].value, test.column, test.value)
			}
		})
	}
}

// A range predicate is not something --values-freq-map can express, so it is
// left out rather than turned into an equality that would be wrong.
func TestParsePredicatesIgnoresRanges(t *testing.T) {
	cond := "(placed_at >= '2026-06-09 00:00:00+00'::timestamp with time zone)"
	if preds := parsePredicates(cond); len(preds) != 0 {
		t.Errorf("parsePredicates(%q) = %v, want nothing", cond, preds)
	}
}

func assertTable(t *testing.T, s *Stats, name string, rows, pages int64) {
	t.Helper()
	for _, table := range s.Tables {
		if table.Table != name {
			continue
		}
		if table.Rows != rows {
			t.Errorf("%s rows = %d, want %d", name, table.Rows, rows)
		}
		if table.Pages != pages {
			t.Errorf("%s pages = %d, want %d", name, table.Pages, pages)
		}
		return
	}
	t.Errorf("%s is missing from the derived tables", name)
}

func assertSelectivity(t *testing.T, s *Stats, table, column, value string, want float64) {
	t.Helper()
	for _, sel := range s.Selectivities {
		if sel.Table != table || sel.Column != column || sel.Value != value {
			continue
		}
		if math.Abs(sel.Fraction-want) > 0.0001 {
			t.Errorf("%s.%s=%s selectivity = %.6f, want %.4f", table, column, value, sel.Fraction, want)
		}
		return
	}
	t.Errorf("%s.%s=%s is missing from the derived selectivities", table, column, value)
}

func assertDistinct(t *testing.T, s *Stats, table, column string, want int64) {
	t.Helper()
	for _, d := range s.Distincts {
		if d.Table == table && d.Column == column {
			if d.Count != want {
				t.Errorf("%s.%s distinct = %d, want %d", table, column, d.Count, want)
			}
			return
		}
	}
	t.Errorf("%s.%s is missing from the derived distinct counts", table, column)
}

func assertFanOut(t *testing.T, s *Stats, child string, want float64) {
	t.Helper()
	for _, f := range s.FanOuts {
		if f.Child == child {
			if math.Abs(f.Rows-want) > 0.01 {
				t.Errorf("%s fan-out = %.2f, want %.2f", child, f.Rows, want)
			}
			return
		}
	}
	t.Errorf("%s is missing from the derived fan-outs", child)
}
