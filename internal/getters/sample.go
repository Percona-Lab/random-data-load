package getters

import (
	"fmt"
	"time"

	"github.com/ylacancellera/random-data-load/db"
)

type ScannedInt struct {
	value int64
}

func (s *ScannedInt) Value() interface{} {
	return s.value
}

func (s *ScannedInt) String() string {
	return fmt.Sprintf("%d", s.Value())
}

func (s *ScannedInt) Quote() string {
	return s.String()
}

func NewScannedInt(v int64) *ScannedInt {
	return &ScannedInt{v}
}

type ScannedString struct {
	value string
}

func (s *ScannedString) Value() interface{} {
	return s.value
}

func (s *ScannedString) String() string {
	return s.value
}

func (s *ScannedString) Quote() string {
	return db.Escape(s.String())
}

func NewScannedString(v string) *ScannedString {
	return &ScannedString{v}
}

type ScannedBinary struct {
	value []rune
}

func (s *ScannedBinary) Value() interface{} {
	return s.value
}

func (s *ScannedBinary) String() string {
	return string(s.value)
}

func (s *ScannedBinary) Quote() string {
	return db.Escape(s.String())
}

func NewScannedBinary(v []rune) *ScannedBinary {
	return &ScannedBinary{v}
}

type ScannedDecimal struct {
	value float64
}

func (s *ScannedDecimal) Value() interface{} {
	return s.value
}

func (s *ScannedDecimal) String() string {
	return fmt.Sprintf("%f", s.Value())
}

func (s *ScannedDecimal) Quote() string {
	return s.String()
}

func NewScannedDecimal(v float64) *ScannedDecimal {
	return &ScannedDecimal{v}
}

type ScannedTime struct {
	value time.Time
}

func (s *ScannedTime) Value() interface{} {
	return s.value
}

func (s *ScannedTime) String() string {
	return fmt.Sprintf("%v", s.Value())
}

func (s *ScannedTime) Quote() string {
	return s.String()
}

func NewScannedTime(v time.Time) *ScannedTime {
	return &ScannedTime{v}
}
