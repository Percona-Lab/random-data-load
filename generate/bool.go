package generate

import (
	"fmt"
	"math/rand"
)

type RandomBool struct {
	value int64
}

func (r *RandomBool) Value() interface{} {
	return r.value
}

func (r *RandomBool) String() string {
	return fmt.Sprintf("%d", r.Value())
}

func (r *RandomBool) Quote() string {
	return fmt.Sprintf("'%d'", r.Value())
}

func NewRandomBool(name string, allowNull bool) *RandomBool {
	return &RandomBool{rand.Int63n(1)}
}
