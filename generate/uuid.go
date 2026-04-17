package generate

import (
	"github.com/google/uuid"
)

type RandomUUID struct {
	value string
}

func (r *RandomUUID) String() string {
	return r.value
}

// Quote returns a quoted string
func (r *RandomUUID) IsQuotable() bool {
	return true
}

func NewRandomUUID(uuidVersion int) *RandomUUID {
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
		// obviously not a graceful handling, but uuid generation error is extremely unlikely
		panic(err)
	}
	s = u.String()
	return &RandomUUID{s}
}
