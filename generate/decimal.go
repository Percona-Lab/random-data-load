package generate

import (
	"fmt"
	"math/rand"
)

// RandomDecimal holds unexported data for decimal values
type RandomDecimal struct {
	value float64
}

func (r *RandomDecimal) String() string {
	return fmt.Sprintf("%0f", r.value)
}

func (r *RandomDecimal) IsQuotable() bool {
	return false
}

func NewRandomDecimal(precision, scale int64) *RandomDecimal {
	f := rand.Float64()
	if precision > 0 {
		f *= float64(rand.Int63n(int64(precision)))
	}
	return &RandomDecimal{f}
}
