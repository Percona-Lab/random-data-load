package generate

import (
	"fmt"
	"math/rand"
)

// RandomDecimal holds unexported data for decimal values
type RandomDecimal struct {
	value float64
}

func (r *RandomDecimal) Value() interface{} {
	return r.value
}

func (r *RandomDecimal) String() string {
	return fmt.Sprintf("%0f", r.Value())
}

func (r *RandomDecimal) Quote() string {
	return r.String()
}

func NewRandomDecimal(name string, precision, scale int64, allowNull bool) *RandomDecimal {
	f := rand.Float64()
	if precision > 0 {
		f *= float64(rand.Int63n(int64(precision)))
	}
	return &RandomDecimal{f}
}
