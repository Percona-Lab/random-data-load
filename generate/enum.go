package generate

import (
	"math/rand"
)

// RandomEnum Getter
type RandomEnum struct {
	value string
}

func (r *RandomEnum) String() string {
	return r.value
}

func (r *RandomEnum) IsQuotable() bool {
	return true
}

func NewRandomEnum(allowedValues []string) *RandomEnum {
	i := rand.Int63n(int64(len(allowedValues)))
	return &RandomEnum{allowedValues[i]}
}
