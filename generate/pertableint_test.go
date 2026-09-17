package generate

import "testing"

func TestParsePerTableInt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  map[string]int64
		err   bool
	}{
		{name: "empty", value: "", want: map[string]int64{}},
		{name: "one number for every table", value: "1000", want: map[string]int64{"": 1000}},
		{name: "per table only", value: "orders=500000;products=20000",
			want: map[string]int64{"orders": 500000, "products": 20000}},
		{name: "a default and one exception", value: "1000;orders=500000",
			want: map[string]int64{"": 1000, "orders": 500000}},
		{name: "commas separate too", value: "1000,orders=500000",
			want: map[string]int64{"": 1000, "orders": 500000}},
		{name: "spaces are trimmed", value: " 1000 ; orders = 5 ",
			want: map[string]int64{"": 1000, "orders": 5}},
		{name: "table names fold to lower case", value: "Orders=7",
			want: map[string]int64{"orders": 7}},
		{name: "two catch-alls are refused", value: "10;20", err: true},
		{name: "a fraction is not a row count", value: "orders=1.5", err: true},
		{name: "nothing on the left", value: "=5", err: true},
		{name: "not a number at all", value: "orders=many", err: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePerTableInt(tc.value)
			if tc.err {
				if err == nil {
					t.Fatalf("ParsePerTableInt(%q) = %v, wanted an error", tc.value, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePerTableInt(%q): %v", tc.value, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("ParsePerTableInt(%q) = %v, want %v", tc.value, got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("ParsePerTableInt(%q)[%q] = %d, want %d", tc.value, k, got[k], v)
				}
			}
		})
	}
}

func TestPerTableIntLookup(t *testing.T) {
	r, err := ParsePerTableInt("1000;orders=500000")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.For("orders"); got != 500000 {
		t.Errorf(`For("orders") = %d, want 500000`, got)
	}
	if got := r.For("ORDERS"); got != 500000 {
		t.Errorf(`For("ORDERS") = %d, want 500000, lookups fold case`, got)
	}
	if got := r.For("products"); got != 1000 {
		t.Errorf(`For("products") = %d, want the catch-all 1000`, got)
	}
	if !r.IsSetFor("anything") {
		t.Error("IsSetFor should be true for any table once a catch-all is given")
	}

	// Named and Tables drop the catch-all: it names no table, and a caller
	// that walks them to learn which tables were spoken about would otherwise
	// invent one called "".
	if names := r.Tables(); len(names) != 1 || names[0] != "orders" {
		t.Errorf("Tables() = %v, want [orders]", names)
	}
	named := r.Named()
	if len(named) != 1 || named["orders"] != 500000 {
		t.Errorf("Named() = %v, want {orders:500000}", named)
	}
	if _, ok := named[""]; ok {
		t.Error(`Named() must not carry the "" catch-all key`)
	}

	// no catch-all: a table nobody named comes back 0, so the caller can tell
	only, err := ParsePerTableInt("orders=5")
	if err != nil {
		t.Fatal(err)
	}
	if got := only.For("products"); got != 0 {
		t.Errorf(`For("products") = %d, want 0 when no catch-all was given`, got)
	}
	if only.IsSetFor("products") {
		t.Error("IsSetFor should be false for a table nobody named")
	}

	only.Set("Products", 9)
	if got := only.For("products"); got != 9 {
		t.Errorf(`after Set("Products", 9), For("products") = %d, want 9`, got)
	}
}

func TestPerTableFloatForColumn(t *testing.T) {
	// --null-freq is written per column, since a table is not the thing that
	// can be NULL.
	r, err := ParsePerTableFloat("0.1;items.tags=0.73;items.price=0")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.ForColumn("items", "tags"); got != 0.73 {
		t.Errorf(`ForColumn("items","tags") = %v, want 0.73`, got)
	}
	if got := r.ForColumn("items", "price"); got != 0 {
		t.Errorf(`ForColumn("items","price") = %v, want an explicit 0`, got)
	}
	if !r.IsSetForColumn("items", "price") {
		t.Error("an explicit 0 must read as set, not as absent")
	}
	if got := r.ForColumn("items", "name"); got != 0.1 {
		t.Errorf(`ForColumn("items","name") = %v, want the catch-all 0.1`, got)
	}
	if got := r.ForColumn("ITEMS", "TAGS"); got != 0.73 {
		t.Errorf("column lookups fold case, got %v", got)
	}
	cols := r.Columns()
	if len(cols) != 2 || cols[0] != "items.price" || cols[1] != "items.tags" {
		t.Errorf("Columns() = %v, want [items.price items.tags] sorted and without the catch-all", cols)
	}
}
