package generate

import (
	"fmt"
	"math/rand"
)

// RandomTime Getter
type RandomTime struct {
	value string
}

func (r *RandomTime) String() string {
	return r.value
}

func (r *RandomTime) IsQuotable() bool {
	return true
}

func NewRandomTime() *RandomTime {
	h := rand.Int63n(24)
	m := rand.Int63n(60)
	s := rand.Int63n(60)
	val := fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	return &RandomTime{val}
}
