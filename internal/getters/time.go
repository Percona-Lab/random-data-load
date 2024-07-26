package getters

import (
	"fmt"
	"math/rand"
)

// RandomTime Getter
type RandomTime struct {
	allowNull bool
}

func (r *RandomTime) Value() interface{} {
	if r.allowNull && rand.Int63n(100) < nilFrequency {
		return nil
	}
	h := rand.Int63n(24)
	m := rand.Int63n(60)
	s := rand.Int63n(60)
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

func (r *RandomTime) String() string {
	value := r.Value()
	if value == nil {
		return NULL
	}
	return value.(string)
}

func (r *RandomTime) Quote() string {
	value := r.Value()
	if value == nil {
		return NULL
	}
	return fmt.Sprintf("'%s'", value)
}

func NewRandomTime(allowNull bool) *RandomTime {
	return &RandomTime{allowNull}
}
