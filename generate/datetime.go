package generate

import (
	"math/rand"
	"time"
)

type RandomDateTimeInRange struct {
	value time.Time
}

func (r *RandomDateTimeInRange) String() string {
	return r.value.Format("2006-01-02 15:03:04")
}

func (r *RandomDateTimeInRange) IsQuotable() bool {
	return true
}

// NewRandomDateTimeInRange returns a new random date in the specified range
//func NewRandomDateTimeInRange(name string, min, max string, allowNull bool) *RandomDateTimeInRange {
//	if min == "" {
//		t := time.Now().Add(-1 * time.Duration(oneYear) * time.Second)
//		min = t.Format("2006-01-02")
//	}
//	return &RandomDateTimeInRange{t}
//}

// NewRandomDateTime returns a new random datetime between Now() and Now() - 1 year
func NewRandomDateTime() *RandomDateTimeInRange {
	randomSeconds := rand.Int63n(oneYear)
	val := time.Now().Add(-1 * time.Duration(randomSeconds) * time.Second)
	return &RandomDateTimeInRange{val}
}
