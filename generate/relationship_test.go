package generate

import (
	"reflect"
	"testing"
)

func TestParseRelationshipFloat(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected RelationshipFloat
	}{
		{name: "one value for every relationship", value: "3", expected: RelationshipFloat{"": 3}},
		{name: "a decimal", value: "0.001", expected: RelationshipFloat{"": 0.001}},
		{name: "per parent", value: "orders=3;products=5", expected: RelationshipFloat{"orders": 3, "products": 5}},
		{name: "a default and an exception", value: "1;orders=3", expected: RelationshipFloat{"": 1, "orders": 3}},
		{name: "commas work too", value: "1,orders=3", expected: RelationshipFloat{"": 1, "orders": 3}},
		{name: "spaces are ignored", value: " 1 ; orders = 3 ", expected: RelationshipFloat{"": 1, "orders": 3}},
		{name: "table names are case insensitive", value: "Orders=3", expected: RelationshipFloat{"orders": 3}},
		{name: "empty", value: "", expected: RelationshipFloat{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseRelationshipFloat(test.value)
			if err != nil {
				t.Fatalf("ParseRelationshipFloat(%q) errored: %v", test.value, err)
			}
			if !reflect.DeepEqual(got, test.expected) {
				t.Errorf("ParseRelationshipFloat(%q) = %v, want %v", test.value, got, test.expected)
			}
		})
	}
}

func TestParseRelationshipFloatRejects(t *testing.T) {
	for _, value := range []string{"abc", "orders=abc", "=3", "1;2", "orders=1;3;4"} {
		if _, err := ParseRelationshipFloat(value); err == nil {
			t.Errorf("ParseRelationshipFloat(%q) should have failed", value)
		}
	}
}

func TestRelationshipFloatFor(t *testing.T) {
	values, err := ParseRelationshipFloat("1;orders=3")
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
func TestRelationshipFloatIsSetFor(t *testing.T) {
	given, err := ParseRelationshipFloat("orders=0")
	if err != nil {
		t.Fatal(err)
	}
	if !given.IsSetFor("orders") {
		t.Error("a value of 0 given for a parent is still a value")
	}
	if given.IsSetFor("customers") {
		t.Error("customers was given nothing, on its own or otherwise")
	}

	var none RelationshipFloat
	if none.IsSetFor("orders") || none.For("orders") != 0 {
		t.Error("a flag left out entirely gives nothing for any parent")
	}
}

func TestRelationshipFloatValues(t *testing.T) {
	values, err := ParseRelationshipFloat("1.1;orders=1.4;products=0.5")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := values.Values(), []float64{1.1, 1.4, 0.5}; !reflect.DeepEqual(got, want) {
		t.Errorf("Values() = %v, want %v: every value has to be checked, not just the default", got, want)
	}
}

func TestNewRelationshipFloat(t *testing.T) {
	values := NewRelationshipFloat(2.5)
	if got := values.For("anything"); got != 2.5 {
		t.Errorf("For(anything) = %g, want 2.5", got)
	}
}
