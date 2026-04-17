package generate

import (
	"fmt"
	"math/rand"
)

type RandomBool struct {
	value int64
}

func (r *RandomBool) String() string {
	return fmt.Sprintf("%d", r.value)
}

func (r *RandomBool) IsQuotable() bool {
	return true // for pg, you can input bool as int when it's quoted
}

func NewRandomBool() *RandomBool {
	return &RandomBool{rand.Int63n(2)}
}
