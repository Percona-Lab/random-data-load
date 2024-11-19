package getters

import (
	"fmt"
	"math/rand"
)

// RandomEnum Getter
type RandomEnum struct {
	value string
	null  bool
}

func (r *RandomEnum) Value() interface{} {
	return r.value
}

func (r *RandomEnum) String() string {
	if !r.null {
		return r.value
	}
	return "NULL"
}

func (r *RandomEnum) Quote() string {
	if v := r.Value(); v != nil {
		return fmt.Sprintf("%q", v)
	}
	return "NULL"
}

func NewRandomEnum(allowedValues []string, allowNull bool) *RandomEnum {
	if allowNull && rand.Int63n(100) < nilFrequency {
		return &RandomEnum{"", true}
	}
	i := rand.Int63n(int64(len(allowedValues)))
	return &RandomEnum{allowedValues[i], false}
}
