package generate

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
	if r.null {
		return NULL
	}
	return r.value
}

func (r *RandomEnum) Quote() string {
	if r.null {
		return NULL
	}
	return fmt.Sprintf("'%s'", r.value)
}

func NewRandomEnum(allowedValues []string, allowNull bool) *RandomEnum {
	if allowNull && rand.Int63n(100) < NullFrequency {
		return &RandomEnum{"", true}
	}
	i := rand.Int63n(int64(len(allowedValues)))
	return &RandomEnum{allowedValues[i], false}
}
