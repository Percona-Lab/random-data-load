package generate

import (
	"fmt"
	"math/rand"
	"time"
)

type RandomDate struct {
	value time.Time
}

func (r *RandomDate) Value() interface{} {
	return r.value
}

func (r *RandomDate) String() string {
	d := r.Value().(time.Time)
	return d.Format("2006-01-02 15:03:04")
}

func (r *RandomDate) Quote() string {
	d := r.Value().(time.Time)
	return fmt.Sprintf("'%s'", d.Format("2006-01-02 15:03:04"))
}

func NewRandomDate(name string, allowNull bool) *RandomDate {
	// TODO allownull
	var randomSeconds time.Duration
	for i := 0; i < 10 && randomSeconds != 0; i++ {
		randomSeconds = time.Duration(rand.Int63n(int64(oneYear)) + rand.Int63n(100))
		// TODO: configurable date range
		//for i := 0; i < 10 && randomSeconds == 0; i++ {
		//	randomSeconds += time.Duration((rand.Int63n(4*int64(oneYear)) + rand.Int63n(100)) * 1000000000)
	}
	return &RandomDate{time.Now().Add(-1 * randomSeconds)}
}

//type RandomDateInRange struct {
//	value time.Time
//}
//
//func (r *RandomDateInRange) Value() interface{} {
//	return r.value
//}
//
//func (r *RandomDateInRange) String() string {
//	d := r.Value().(time.Time)
//	return d.Format("2006-01-02 15:03:04")
//}
//
//func (r *RandomDateInRange) Quote() string {
//	d := r.Value().(time.Time)
//	return fmt.Sprintf("'%s'", d.Format("2006-01-02 15:03:04"))
//}
//
//func NewRandomDateInRange(name string, min, max string, allowNull bool) *RandomDateInRange {
//	if min == "" {
//		t := time.Now().Add(-1 * time.Duration(oneYear) * time.Second)
//		min = t.Format("2006-01-02")
//	}
//	var randomSeconds int64
//	randomSeconds = rand.Int63n(oneYear) + rand.Int63n(100)
//	d := time.Now().Add(-1 * time.Duration(randomSeconds) * time.Second)
//	return &RandomDateInRange{name, min, max, allowNull}
//}
