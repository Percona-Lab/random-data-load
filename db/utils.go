package db

import (
	"strings"

	"slices"

	"github.com/rs/zerolog/log"
)

// sort the tables so that dependencies are inserted first
func SortTables(tables []*Table) []*Table {

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
	}
	return tablesSorted
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

func isSliceSimilar(s1, s2 []string) bool {
	for _, e := range s1 {
		if !slices.ContainsFunc(s2, func(s string) bool { return strings.ToLower(s) == strings.ToLower(e) }) {
			return false
		}
	}
	return true
}
