package generate

import (
	"github.com/icrowley/fake"
)

// RandomBinary getter
type RandomBinary struct {
	value *string
}

func (r *RandomBinary) String() string {
	return *r.value
}

func (r *RandomBinary) IsQuotable() bool {
	return true
}

func NewRandomBinary(maxSize int64) *RandomBinary {
	var s string
	//maxSize := uint64(r.maxSize)
	//if maxSize == 0 {
	//	maxSize = uint64(rand.Int63n(100))
	//}

	s = fake.Sentence()
	if len(s) > int(maxSize) {
		s = s[:int(maxSize)]
	}
	return &RandomBinary{&s}
}
