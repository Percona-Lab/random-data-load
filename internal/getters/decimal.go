package getters

import (
	"fmt"
	"math/rand"
)

// RandomDecimal holds unexported data for decimal values
type RandomDecimal struct {
	name      string
	precision int64
	scale     int64
	allowNull bool
}

func (r *RandomDecimal) Value() interface{} {
	f := rand.Float64()
	if r.precision > 0 {
		f *= float64(rand.Int63n(int64(r.precision)))
	}
	return f
}

func (r *RandomDecimal) String() string {
	return fmt.Sprintf("%0f", r.Value())
}

func (r *RandomDecimal) Quote() string {
	return r.String()
}

func NewRandomDecimal(name string, precision, scale int64, allowNull bool) *RandomDecimal {
	return &RandomDecimal{name, precision, scale, allowNull}
}
