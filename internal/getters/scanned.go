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

func (s *ScannedInt) Scan(src any) (err error) {
	switch x := src.(type) {
	case int64:
		s.value = x
	default:
		err = fmt.Errorf("unsupported scan type %T", src)
	}
	return
}

func NewScannedInt() *ScannedInt {
	return &ScannedInt{}
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

func (s *ScannedString) Scan(src any) (err error) {
	switch x := src.(type) {
	case string:
		s.value = x
	default:
		err = fmt.Errorf("unsupported scan type %T", src)
	}
	return
}

func NewScannedString() *ScannedString {
	return &ScannedString{}
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

func (s *ScannedBinary) Scan(src any) (err error) {
	switch x := src.(type) {
	case []rune:
		s.value = x
	default:
		err = fmt.Errorf("unsupported scan type %T", src)
	}
	return
}

func NewScannedBinary() *ScannedBinary {
	return &ScannedBinary{}
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

func (s *ScannedDecimal) Scan(src any) (err error) {
	switch x := src.(type) {
	case float64:
		s.value = x
	default:
		err = fmt.Errorf("unsupported scan type %T", src)
	}
	return
}

func NewScannedDecimal() *ScannedDecimal {
	return &ScannedDecimal{}
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

func (s *ScannedTime) Scan(src any) (err error) {
	switch x := src.(type) {
	case time.Time:
		s.value = x
	default:
		err = fmt.Errorf("unsupported scan type %T", src)
	}
	return
}

func NewScannedTime() *ScannedTime {
	return &ScannedTime{}
}
