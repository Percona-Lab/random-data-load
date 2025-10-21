package generate

import (
	"fmt"
	"math/rand"

	"github.com/google/uuid"
)

type RandomUUID struct {
	value string
	null  bool
}

func (r *RandomUUID) Value() interface{} {
	if r.null {
		return NULL
	}
	return r.value
}

func (r *RandomUUID) String() string {
	return r.Value().(string)
}

// Quote returns a quoted string
func (r *RandomUUID) Quote() string {
	if r.null {
		return NULL
	}
	return fmt.Sprintf("'%s'", r.value)
}

func NewRandomUUID(name string, uuidVersion int, allowNull bool) *RandomUUID {

	var (
		s   string
		err error
		u   uuid.UUID
	)
	switch uuidVersion {
	case 7:
		u, err = uuid.NewV7()
	case 4:
		fallthrough
	default:
		u, err = uuid.NewRandom()
	}
	if err != nil {
		if allowNull {
			return &RandomUUID{"", true}
		}
		// obviously not a graceful handling, but uuid generation error is extremely unlikely
		panic(err)
	}
	s = u.String()
	if allowNull && rand.Int63n(100) < nilFrequency {
		return &RandomUUID{"", true}
	}
	return &RandomUUID{s, false}
}
