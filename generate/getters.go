package generate

import (
	"database/sql"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/ylacancellera/random-data-load/db"
	"github.com/ylacancellera/random-data-load/frequency"
)

type Getter interface {
	IsQuotable() bool
	String() string
}

type ScannerGetter interface {
	Getter
	sql.Scanner
}

const (
	oneYear = int64(60 * 60 * 24 * 365)
	NULL    = "NULL"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

type Null struct{}

func (_ *Null) String() string {
	return NULL
}
func (_ *Null) IsQuotable() bool {
	return false
}

type InsertValues []Getter

// Render writes one row of an INSERT.
//
// A column left unset is reported rather than written: it means generating or
// sampling it did not happen, and rendering it used to be a nil dereference
// that took the whole process down, with an exit status of 0 and no row
// inserted.
func (iv InsertValues) Render() (string, error) {
	var query strings.Builder
	query.WriteString("(")

	sep := ""
	for i, v := range iv {
		if isUnset(v) {
			return "", errors.Errorf("column %d of a row was left unfilled, so the row cannot be inserted", i+1)
		}
		query.WriteString(sep + v.String())
		sep = ", "
	}
	query.WriteString(")")

	return query.String(), nil
}

// isUnset reports whether nothing was ever assigned to this column, either
// because no getter was stored for it or because the wrapper standing in for it
// stayed empty.
func isUnset(g Getter) bool {
	if g == nil {
		return true
	}
	if gw, ok := g.(*GetterWrapper); ok {
		return gw == nil || gw.Elem == nil
	}
	return false
}

type GetterWrapper struct {
	Elem Getter
}

func NewGetterWrapper(column string, isNullable bool, freq frequency.ColumnFrequency) *GetterWrapper {
	wrapper := GetterWrapper{}
	if freq.Null(column, isNullable) {
		wrapper.Elem = &Null{}
	}
	value, ok := freq.InjectIndexValue(column)
	if ok {
		// These values are not generated, they are copied verbatim from a
		// query, a --values-freq-map or a pg_stats dump, so they can carry
		// anything the source column held.
		wrapper.Elem = &RandomString{value: db.EscapeValue(value)}
	}

	return &wrapper
}

func (gw *GetterWrapper) Assign(g Getter) {
	if gw.Elem != nil {
		return
	}

	gw.Elem = g
}

func (gw *GetterWrapper) String() string {
	if gw.Elem.IsQuotable() {
		return fmt.Sprintf("'%v'", gw.Elem)
	}
	return fmt.Sprintf("%v", gw.Elem)
}

func (gw *GetterWrapper) IsQuotable() bool {
	return gw.Elem.IsQuotable()
}
