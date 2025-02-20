package generate

import (
	"database/sql"
	"math/rand"
	"time"
)

// All types defined here satisfy the Getter interface
type Getter interface {
	Value() interface{}
	Quote() string
	String() string
}

type ScannerGetter interface {
	Getter
	sql.Scanner
}

const (
	nilFrequency = 10
	oneYear      = int64(60 * 60 * 24 * 365)
	NULL         = "NULL"
)

type Null struct{}

func (_ *Null) Value() interface{} {
	return NULL
}
func (_ *Null) Quote() string {
	return NULL
}
func (_ *Null) String() string {
	return NULL
}

type InsertValues []Getter

func (iv InsertValues) String() string {
	sep := ""
	query := "("

	for _, v := range iv {
		// wherever it failed, continue optimistically
		if v == nil {
			v = &Null{}
		}
		query += sep + v.Quote()
		sep = ", "
	}
	query += ")"

	return query
}

func init() {
	rand.Seed(time.Now().UnixNano())
}
