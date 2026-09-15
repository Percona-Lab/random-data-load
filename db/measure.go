package db

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// Storage is what the catalog says a filled table takes on disk.
//
// Pages is the figure a plan's scan cost is built from, so it is the one that
// has to match for a reproduction to hold. It is only as fresh as the last
// ANALYZE, which is why Analyze exists next to it.
type Storage struct {
	Pages  int64
	Tuples int64
	Width  int64  // average bytes per row, summed over the columns
	Note   string // why a figure is approximate
}

// Predicate is one "column = value" a plan filters on.
type Predicate struct {
	Column string
	Value  string
}

// Key names a predicate in a result map.
func (p Predicate) Key() string { return p.Column + "=" + p.Value }

// TableMeasure is everything one table is asked about in a single pass.
type TableMeasure struct {
	Schema, Table string
	Predicates    []Predicate
	Distincts     []string
	Nulls         []string
}

// TableMeasured is what the table answered. A figure missing from a map could
// not be read, and Errors says why.
type TableMeasured struct {
	Rows     int64
	Matching map[string]int64
	Distinct map[string]int64
	Nulls    map[string]int64
	Errors   []string
}

// Analyze refreshes the statistics the catalog reports for a table.
//
// A table filled a moment ago has no statistics at all: postgres reports zero
// pages and -1 tuples until something collects them, and comparing a generated
// table against a reported plan means comparing the figures a planner would
// read, not the ones it happens to have cached.
func Analyze(schema, table string) error {
	return engine.Analyze(schema, table)
}

// TableStorage reads the page and row counts the catalog holds.
func TableStorage(schema, table string) (Storage, error) {
	return engine.TableStorage(schema, table)
}

// Measure reads a table once for every figure asked of it.
//
// One statement rather than one per figure: the tables being checked are the
// large ones by definition, and every aggregate here needs the same full scan.
// A statement the engine refuses is retried figure by figure, so one column it
// cannot compare does not cost the whole table its report.
func Measure(m TableMeasure) TableMeasured {
	measured := TableMeasured{
		Matching: map[string]int64{},
		Distinct: map[string]int64{},
		Nulls:    map[string]int64{},
	}

	exprs, assign := measureExpressions(m, &measured)
	query := fmt.Sprintf("SELECT %s FROM %s.%s", strings.Join(exprs, ", "), Escape(m.Schema), Escape(m.Table))

	values := make([]sql.NullInt64, len(exprs))
	recipients := make([]interface{}, len(exprs))
	for i := range values {
		recipients[i] = &values[i]
	}

	log.Debug().Str("query", query).Msg("measuring a generated table")
	if err := DB.QueryRow(query).Scan(recipients...); err != nil {
		log.Debug().Err(err).Str("query", query).Msg("reading every figure at once failed, reading them one by one")
		measureOneByOne(m, exprs, assign, &measured)
		return measured
	}
	for i, value := range values {
		if value.Valid {
			assign[i](value.Int64)
		}
	}
	return measured
}

// measureExpressions pairs each aggregate with where its answer belongs, so
// that the two lists cannot drift apart.
func measureExpressions(m TableMeasure, measured *TableMeasured) ([]string, []func(int64)) {
	exprs := []string{"count(*)"}
	assign := []func(int64){func(v int64) { measured.Rows = v }}

	for _, predicate := range m.Predicates {
		key := predicate.Key()
		exprs = append(exprs, fmt.Sprintf("sum(CASE WHEN %s = '%s' THEN 1 ELSE 0 END)",
			Escape(predicate.Column), EscapeValue(predicate.Value)))
		assign = append(assign, func(v int64) { measured.Matching[key] = v })
	}
	for _, column := range m.Distincts {
		col := column
		exprs = append(exprs, fmt.Sprintf("count(DISTINCT %s)", Escape(col)))
		assign = append(assign, func(v int64) { measured.Distinct[col] = v })
	}
	for _, column := range m.Nulls {
		col := column
		exprs = append(exprs, fmt.Sprintf("sum(CASE WHEN %s IS NULL THEN 1 ELSE 0 END)", Escape(col)))
		assign = append(assign, func(v int64) { measured.Nulls[col] = v })
	}
	return exprs, assign
}

func measureOneByOne(m TableMeasure, exprs []string, assign []func(int64), measured *TableMeasured) {
	for i, expr := range exprs {
		query := fmt.Sprintf("SELECT %s FROM %s.%s", expr, Escape(m.Schema), Escape(m.Table))
		var value sql.NullInt64
		if err := DB.QueryRow(query).Scan(&value); err != nil {
			measured.Errors = append(measured.Errors,
				errors.Wrapf(err, "%s.%s: %s", m.Schema, m.Table, expr).Error())
			continue
		}
		if value.Valid {
			assign[i](value.Int64)
		}
	}
}
