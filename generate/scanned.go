package generate

import (
	"fmt"
	"time"

	"github.com/ylacancellera/random-data-load/db"
)

type ScannedInt struct {
	value int64
}

func (s *ScannedInt) String() string {
	return fmt.Sprintf("%d", s.value)
}

func (s *ScannedInt) IsQuotable() bool {
	return false
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

func (s *ScannedString) String() string {
	return s.value
}

func (s *ScannedString) IsQuotable() bool {
	return true
}

func (s *ScannedString) Scan(src any) (err error) {
	switch x := src.(type) {
	case string:
		s.value = x
	case []uint8:
		s.value = string(x)
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

func (s *ScannedBinary) String() string {
	// TODO: move db.escape upper for every fields ?
	return db.Escape(string(s.value))
}

func (s *ScannedBinary) IsQuotable() bool {
	return true
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

func (s *ScannedDecimal) String() string {
	return fmt.Sprintf("%f", s.value)
}

func (s *ScannedDecimal) IsQuotable() bool {
	return false
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

func (s *ScannedTime) String() string {
	return fmt.Sprintf("%v", s.value)
}

func (s *ScannedTime) IsQuotable() bool {
	return true
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
