package getters

import (
	"fmt"
	"math/rand"

	"github.com/icrowley/fake"
)

// RandomBinary getter
type RandomBinary struct {
	value *string
}

func (r *RandomBinary) Value() interface{} {
	return *r.value
}

func (r *RandomBinary) String() string {
	v := r.Value()
	if v == nil {
		return NULL
	}
	return v.(string)
}

func (r *RandomBinary) Quote() string {
	v := r.Value()
	if v == nil {
		return NULL
	}
	return fmt.Sprintf("%q", v)
}

func NewRandomBinary(name string, maxSize int64, allowNull bool) *RandomBinary {
	if allowNull && rand.Int63n(100) < nilFrequency {
		return &RandomBinary{}
	}
	var s string
	//maxSize := uint64(r.maxSize)
	//if maxSize == 0 {
	//	maxSize = uint64(rand.Int63n(100))
	//}

	if maxSize <= 10 {
		s = fake.FirstName()
	} else if maxSize < 30 {
		s = fake.FullName()
	} else {
		s = fake.Sentence()
	}
	if len(s) > int(maxSize) {
		s = s[:int(maxSize)]
	}
	return &RandomBinary{&s}
}
