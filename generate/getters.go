package generate

import (
	"database/sql"
	"fmt"
	"math/rand"
	"time"

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

func (iv InsertValues) String() string {
	sep := ""
	query := "("

	for _, v := range iv {
		query += sep + v.String()
		sep = ", "
	}
	query += ")"

	return query
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
		wrapper.Elem = &RandomString{value: value}
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
	return gw.IsQuotable()
}
