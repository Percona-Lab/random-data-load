package generate

import (
	"time"
)

type RandomDate struct {
	value time.Time
}

func (r *RandomDate) String() string {
	return r.value.Format("2006-01-02 15:03:04")
}

func (r *RandomDate) IsQuotable() bool {
	return true
}

func NewRandomDate(minGeneratedTime, maxGeneratedTime *time.Time) *RandomDate {
	t := time.Unix(NewRandomIntRange(minGeneratedTime.Unix(), maxGeneratedTime.Unix()).value, 0)
	return &RandomDate{t}
}
