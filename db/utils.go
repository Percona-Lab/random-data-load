package db

import (
	"strings"

	"slices"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// sort the tables so that dependencies are inserted first
//
// It fails when what is left cannot be ordered at all: tables waiting on each
// other in a loop have no insert order satisfying them.
func SortTables(tables []*Table) ([]*Table, error) {

	slices.SortFunc(tables, func(a, b *Table) int {
		return len(a.Constraints) - len(b.Constraints)
	})
	tablesSorted := make([]*Table, 0, cap(tables))
	tablesIndexes := make([]int, len(tables), cap(tables))

	// we get a slice for indexes of the main "tables" slices
	// we want to keep the "tables" untouched and reorganize it, tablesIndexes will track what is left to handle
	for i := 0; i < len(tables); i++ {
		tablesIndexes[i] = i
	}

INSERT_LOOP:
	for len(tablesIndexes) > 0 {
		for metaIndex, idx := range tablesIndexes {
			if tables[idx].AreAllDependenciesContained(tablesSorted) {
				log.Debug().Str("table", tables[idx].Name).Msg("all dep are contained, adding to running order")
				tablesSorted = append(tablesSorted, tables[idx])
				tablesIndexes = slices.Delete(tablesIndexes, metaIndex, metaIndex+1)
				continue INSERT_LOOP
			}
			log.Debug().Str("table", tables[idx].Name).Msg("not all deps are contained, continue")
		}

		// A whole pass placed nothing, so every table left waits on another
		// one still waiting: a new pass would read the same list again.
		remaining := make([]string, 0, len(tablesIndexes))
		for _, idx := range tablesIndexes {
			remaining = append(remaining, tables[idx].Name)
		}
		return nil, errors.Errorf("tables %s depend on each other in a loop, there is no order in which they can be inserted. Foreign keys guessed from a --query can be turned off with --no-fk-guess, and declared one by one with --add-fk", strings.Join(remaining, ", "))
	}
	return tablesSorted, nil
}

func EscapedNamesListFromFields(fields []Field) string {
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, Escape(field.ColumnName))
	}
	return strings.Join(names, ",")
}

func EscapedFieldsIsNotNull(fields []Field) string {
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, Escape(field.ColumnName)+" IS NOT NULL")
	}
	return strings.Join(names, " AND ")
}
