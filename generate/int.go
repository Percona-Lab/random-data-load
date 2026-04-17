package generate

import (
	"fmt"
	"math/rand"
)

type RandomInt struct {
	value int64
}

func (r *RandomInt) String() string {
	return fmt.Sprintf("%d", r.value)
}

func (r *RandomInt) IsQuotable() bool {
	return false
}

func NewRandomInt(mask int64) *RandomInt {
	return &RandomInt{rand.Int63n(mask)}
}

type RandomIntRange struct {
	value int64
}

func (r *RandomIntRange) String() string {
	return fmt.Sprintf("%d", r.value)
}

func (r *RandomIntRange) IsQuotable() bool {
	return false
}

func NewRandomIntRange(min, max int64) *RandomIntRange {
	limit := max - min + 1
	return &RandomIntRange{min + rand.Int63n(limit)}
}
