package generate

import (
	"fmt"
	"math/rand"
)

// RandomTime Getter
type RandomTime struct {
	value string
	null  bool
}

func (r *RandomTime) Value() interface{} {
	if r.null {
		return NULL
	}
	return r.value
}

func (r *RandomTime) String() string {
	if r.null {
		return NULL
	}
	return r.value
}

func (r *RandomTime) Quote() string {
	if r.null {
		return NULL
	}
	return fmt.Sprintf("'%s'", r.value)
}

func NewRandomTime(allowNull bool) *RandomTime {
	if allowNull && rand.Int63n(100) < NullFrequency {
		return &RandomTime{"", true}
	}
	h := rand.Int63n(24)
	m := rand.Int63n(60)
	s := rand.Int63n(60)
	val := fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	return &RandomTime{val, false}
}
