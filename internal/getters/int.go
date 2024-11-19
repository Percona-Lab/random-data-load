package getters

import (
	"fmt"
	"math/rand"
)

type RandomInt struct {
	value int64
}

func (r *RandomInt) Value() interface{} {
	return r.value
}

func (r *RandomInt) String() string {
	return fmt.Sprintf("%d", r.Value())
}

func (r *RandomInt) Quote() string {
	return r.String()
}

func NewRandomInt(name string, mask int64, allowNull bool) *RandomInt {
	return &RandomInt{rand.Int63n(mask)}
}

type RandomIntRange struct {
	value int64
}

func (r *RandomIntRange) Value() interface{} {
	return r.value
}

func (r *RandomIntRange) String() string {
	return fmt.Sprintf("%d", r.Value())
}

func (r *RandomIntRange) Quote() string {
	return r.String()
}

func NewRandomIntRange(name string, min, max int64, allowNull bool) *RandomIntRange {
	limit := max - min + 1
	return &RandomIntRange{min + rand.Int63n(limit)}
}
