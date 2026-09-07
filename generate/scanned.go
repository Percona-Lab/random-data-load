package generate

import (
	"fmt"
	"strconv"
	"time"

	"github.com/Percona-Lab/random-data-load/db"
)

// The Scanned* getters read a parent row and hand its value back verbatim to
// the child's INSERT, so a foreign key matches. Every one of them accepts more
// than one Go type for the same column type on purpose: a driver decides for
// itself what it hands over. lib/pq answers []uint8 for uuid, numeric, enums
// and anything it has no dedicated type for, and go-sql-driver answers []uint8
// for a decimal. A type left out here used to reach rows.Scan as a nil
// destination and take the process down with it.

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
	case int32:
		s.value = int64(x)
	case float64:
		s.value = int64(x)
	case []uint8:
		s.value, err = strconv.ParseInt(string(x), 10, 64)
	case string:
		s.value, err = strconv.ParseInt(x, 10, 64)
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

// String escapes the value it read. A sampled value is production data, not
// something this tool generated, so it routinely holds a quote.
func (s *ScannedString) String() string {
	return db.EscapeValue(s.value)
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
	case int64:
		s.value = strconv.FormatInt(x, 10)
	case float64:
		s.value = strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		s.value = strconv.FormatBool(x)
	case time.Time:
		s.value = x.Format(db.ValueTimeLayout())
	default:
		err = fmt.Errorf("unsupported scan type %T", src)
	}
	return
}

func NewScannedString() *ScannedString {
	return &ScannedString{}
}

type ScannedBinary struct {
	value string
}

func (s *ScannedBinary) String() string {
	return db.EscapeValue(s.value)
}

func (s *ScannedBinary) IsQuotable() bool {
	return true
}

func (s *ScannedBinary) Scan(src any) (err error) {
	switch x := src.(type) {
	case []uint8:
		s.value = string(x)
	case []rune:
		s.value = string(x)
	case string:
		s.value = x
	default:
		err = fmt.Errorf("unsupported scan type %T", src)
	}
	return
}

func NewScannedBinary() *ScannedBinary {
	return &ScannedBinary{}
}

// ScannedDecimal keeps the value as the database wrote it rather than as a
// float64. A numeric(30,10) key does not survive a float64, and a key only
// matches its parent if every digit does.
type ScannedDecimal struct {
	value string
}

func (s *ScannedDecimal) String() string {
	return s.value
}

func (s *ScannedDecimal) IsQuotable() bool {
	return false
}

func (s *ScannedDecimal) Scan(src any) (err error) {
	switch x := src.(type) {
	case []uint8:
		s.value = string(x)
	case string:
		s.value = x
	case float64:
		s.value = strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		s.value = strconv.FormatFloat(float64(x), 'f', -1, 32)
	case int64:
		s.value = strconv.FormatInt(x, 10)
	default:
		err = fmt.Errorf("unsupported scan type %T", src)
	}
	return
}

func NewScannedDecimal() *ScannedDecimal {
	return &ScannedDecimal{}
}

// ScannedBool renders as a quoted 0 or 1, the one spelling both engines read:
// postgres casts '0' and '1' to boolean, and mysql stores them in the tinyint
// a bool really is. It matches what NewRandomBool generates.
type ScannedBool struct {
	value int64
}

func (s *ScannedBool) String() string {
	return fmt.Sprintf("%d", s.value)
}

func (s *ScannedBool) IsQuotable() bool {
	return true
}

func (s *ScannedBool) Scan(src any) (err error) {
	var b bool
	switch x := src.(type) {
	case bool:
		b = x
	case int64:
		b = x != 0
	case []uint8:
		b, err = strconv.ParseBool(string(x))
	case string:
		b, err = strconv.ParseBool(x)
	default:
		err = fmt.Errorf("unsupported scan type %T", src)
	}
	if b {
		s.value = 1
	}
	return
}

func NewScannedBool() *ScannedBool {
	return &ScannedBool{}
}

type ScannedTime struct {
	value time.Time
}

func (s *ScannedTime) String() string {
	return s.value.Format(db.ValueTimeLayout())
}

func (s *ScannedTime) IsQuotable() bool {
	return true
}

func (s *ScannedTime) Scan(src any) (err error) {
	switch x := src.(type) {
	case time.Time:
		s.value = x
	case []uint8:
		s.value, err = parseScannedTime(string(x))
	case string:
		s.value, err = parseScannedTime(x)
	default:
		err = fmt.Errorf("unsupported scan type %T", src)
	}
	return
}

// parseScannedTime covers the shapes a driver hands over as text when it was
// not asked to parse dates itself.
func parseScannedTime(s string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999-07",
		"2006-01-02 15:04:05.999999",
		"2006-01-02",
		"15:04:05.999999",
	}
	var err error
	for _, layout := range layouts {
		var t time.Time
		if t, err = time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot read %q as a date: %s", s, err)
}

func NewScannedTime() *ScannedTime {
	return &ScannedTime{}
}
