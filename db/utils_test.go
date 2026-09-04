package db

import "testing"

// tableWithConstraintsTo builds a table pointing at each of the given tables,
// as a run that has to satisfy those keys would see it.
func tableWithConstraintsTo(name string, referenced ...*Table) *Table {
	table := &Table{Schema: "test", Name: name}
	for _, ref := range referenced {
		table.Constraints = append(table.Constraints, &Constraint{
			ConstraintName:              name + "_" + ref.Name,
			TableName:                   name,
			ReferencedTableSchema:       "test",
			ReferencedTableName:         ref.Name,
			ReferencedTable:             ref,
			willBeInsertedDuringThisRun: true,
		})
	}
	return table
}

func TestSortTables(t *testing.T) {
	t.Run("orders dependencies first", func(t *testing.T) {
		t1 := tableWithConstraintsTo("t1")
		t2 := tableWithConstraintsTo("t2", t1)
		t3 := tableWithConstraintsTo("t3", t2)

		sorted, err := SortTables([]*Table{t3, t2, t1})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var order []string
		for _, table := range sorted {
			order = append(order, table.Name)
		}
		if len(order) != 3 || order[0] != "t1" || order[1] != "t2" || order[2] != "t3" {
			t.Fatalf("expected t1, t2, t3, got %v", order)
		}
	})

	// Tables waiting on each other used to send SortTables spinning: a pass
	// took nothing out of the list, and the next one read the same list again.
	t.Run("reports a loop instead of looping", func(t *testing.T) {
		t1 := tableWithConstraintsTo("t1")
		t2 := tableWithConstraintsTo("t2", t1)
		t1.Constraints = tableWithConstraintsTo("t1", t2).Constraints

		sorted, err := SortTables([]*Table{t1, t2})
		if err == nil {
			t.Fatalf("expected a loop to be reported, got an order of %d tables", len(sorted))
		}
		if sorted != nil {
			t.Fatalf("expected no order along with the error, got %v", sorted)
		}
	})
}

func TestConstraintIsLooping(t *testing.T) {
	t.Run("a key closing a loop", func(t *testing.T) {
		t1 := tableWithConstraintsTo("t1")
		t2 := tableWithConstraintsTo("t2", t1)
		// t1 pointing back at t2 leaves neither table insertable first
		t1.Constraints = tableWithConstraintsTo("t1", t2).Constraints

		if !t1.Constraints[0].IsLooping() {
			t.Fatal("expected the key closing the loop to be reported as looping")
		}
		if !t2.Constraints[0].IsLooping() {
			t.Fatal("expected the other key of the loop to be reported as looping")
		}
	})

	t.Run("a key pointing at a dependency", func(t *testing.T) {
		t1 := tableWithConstraintsTo("t1")
		t2 := tableWithConstraintsTo("t2", t1)

		if t2.Constraints[0].IsLooping() {
			t.Fatal("expected a plain dependency not to be reported as looping")
		}
	})

	// A self-reference is resolved by inserting to the table twice, so it
	// neither loops itself nor makes the keys pointing at that table loop.
	t.Run("a self-reference", func(t *testing.T) {
		t1 := &Table{Schema: "test", Name: "t1"}
		t1.Constraints = tableWithConstraintsTo("t1", t1).Constraints
		t2 := tableWithConstraintsTo("t2", t1)

		if !t1.Constraints[0].IsSelfReferencing() {
			t.Fatal("expected the key to be reported as self-referencing")
		}
		if t1.Constraints[0].IsLooping() {
			t.Fatal("expected a self-reference not to be reported as looping")
		}
		if t2.Constraints[0].IsLooping() {
			t.Fatal("expected a key pointing at a self-referencing table not to be reported as looping")
		}
	})
}
