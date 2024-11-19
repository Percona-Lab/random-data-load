package getters

import (
	"math/rand"
	"time"
)

// All types defined here satisfy the Getter interface
type Getter interface {
	Value() interface{}
	Quote() string
	String() string
}

const (
	nilFrequency = 10
	oneYear      = int64(60 * 60 * 24 * 365)
	NULL         = "NULL"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}
