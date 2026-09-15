package generate

import (
	"reflect"
	"testing"
)

func TestParsePerTableFloat(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected PerTableFloat
	}{
		{name: "one value for every relationship", value: "3", expected: PerTableFloat{"": 3}},
		{name: "a decimal", value: "0.001", expected: PerTableFloat{"": 0.001}},
		{name: "per parent", value: "orders=3;products=5", expected: PerTableFloat{"orders": 3, "products": 5}},
		{name: "a default and an exception", value: "1;orders=3", expected: PerTableFloat{"": 1, "orders": 3}},
		{name: "commas work too", value: "1,orders=3", expected: PerTableFloat{"": 1, "orders": 3}},
		{name: "spaces are ignored", value: " 1 ; orders = 3 ", expected: PerTableFloat{"": 1, "orders": 3}},
		{name: "table names are case insensitive", value: "Orders=3", expected: PerTableFloat{"orders": 3}},
		{name: "empty", value: "", expected: PerTableFloat{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParsePerTableFloat(test.value)
			if err != nil {
				t.Fatalf("ParsePerTableFloat(%q) errored: %v", test.value, err)
			}
			if !reflect.DeepEqual(got, test.expected) {
				t.Errorf("ParsePerTableFloat(%q) = %v, want %v", test.value, got, test.expected)
			}
		})
	}
}

func TestParsePerTableFloatRejects(t *testing.T) {
	for _, value := range []string{"abc", "orders=abc", "=3", "1;2", "orders=1;3;4"} {
		if _, err := ParsePerTableFloat(value); err == nil {
			t.Errorf("ParsePerTableFloat(%q) should have failed", value)
		}
	}
}

func TestPerTableFloatFor(t *testing.T) {
	values, err := ParsePerTableFloat("1;orders=3")
	if err != nil {
		t.Fatal(err)
	}

	if got := values.For("orders"); got != 3 {
		t.Errorf("For(orders) = %g, want the value it was given, 3", got)
	}
	if got := values.For("ORDERS"); got != 3 {
		t.Errorf("For(ORDERS) = %g, want 3: a table name does not change with its case", got)
	}
	if got := values.For("customers"); got != 1 {
		t.Errorf("For(customers) = %g, want the value given for every relationship, 1", got)
	}
}

// A sampler works its own value out when none was given, so it has to be able
// to tell that apart from a value of 0.
func TestPerTableFloatIsSetFor(t *testing.T) {
	given, err := ParsePerTableFloat("orders=0")
	if err != nil {
		t.Fatal(err)
	}
	if !given.IsSetFor("orders") {
		t.Error("a value of 0 given for a parent is still a value")
	}
	if given.IsSetFor("customers") {
		t.Error("customers was given nothing, on its own or otherwise")
	}

	var none PerTableFloat
	if none.IsSetFor("orders") || none.For("orders") != 0 {
		t.Error("a flag left out entirely gives nothing for any parent")
	}
}

func TestPerTableFloatValues(t *testing.T) {
	values, err := ParsePerTableFloat("1.1;orders=1.4;products=0.5")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := values.Values(), []float64{1.1, 1.4, 0.5}; !reflect.DeepEqual(got, want) {
		t.Errorf("Values() = %v, want %v: every value has to be checked, not just the default", got, want)
	}
}

func TestNewPerTableFloat(t *testing.T) {
	values := NewPerTableFloat(2.5)
	if got := values.For("anything"); got != 2.5 {
		t.Errorf("For(anything) = %g, want 2.5", got)
	}
}
