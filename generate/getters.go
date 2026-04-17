package generate

import (
	"database/sql"
	"fmt"
	"math/rand"
	"time"
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

func NewGetterWrapper(column string, nullFreq int) *GetterWrapper {
	wrapper := GetterWrapper{}
	if nullFreq > 0 && rand.Int63n(100) < int64(nullFreq) {
		wrapper.Elem = &Null{}
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

func init() {
	rand.Seed(time.Now().UnixNano())
}
