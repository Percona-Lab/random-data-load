package generate

import (
	"database/sql"
	"fmt"
	"math/rand"
	"strconv"
)

// maxDecimalDigits is as many digits as an int64 can count without wrapping.
// postgres allows a numeric up to 1000 digits wide, which no fixed-width
// integer can draw in one piece; a value narrower than the column asked for is
// always one the column accepts, so the draw is capped here instead.
const maxDecimalDigits = 18

// RandomDecimal holds a decimal value as text.
//
// The value is kept as it will be written rather than as a float64: the digits
// a numeric(p,s) column accepts are decided by p and s, and rounding a float
// to them at print time is what used to put values out of range.
type RandomDecimal struct {
	text string
}

func (r *RandomDecimal) String() string {
	return r.text
}

func (r *RandomDecimal) IsQuotable() bool {
	return false
}

// NewRandomDecimal draws a value the column can hold.
//
// numeric(p,s) keeps p significant digits, s of them after the point, so its
// largest value is 10^(p-s) - 10^-s.
//
// Only an exact numeric reports a scale. float, double precision and real
// report a binary precision and no scale, so they keep an approximate value:
// counting digits would be meaningless for them, and a double precision column
// reporting 53 would otherwise be read as 53 decimal digits.
func NewRandomDecimal(precision, scale sql.NullInt64) *RandomDecimal {
	if !precision.Valid || !scale.Valid || precision.Int64 <= 0 {
		return &RandomDecimal{strconv.FormatFloat(approximateDecimal(precision), 'f', 6, 64)}
	}

	scaleDigits := scale.Int64
	if scaleDigits < 0 {
		scaleDigits = 0
	}
	integerDigits := precision.Int64 - scaleDigits
	if integerDigits < 0 {
		integerDigits = 0
	}

	integerPart := int64(0)
	if integerDigits > 0 {
		integerPart = rand.Int63n(pow10(integerDigits))
	}
	if scaleDigits == 0 {
		return &RandomDecimal{strconv.FormatInt(integerPart, 10)}
	}
	if scaleDigits > maxDecimalDigits {
		scaleDigits = maxDecimalDigits
	}
	fraction := rand.Int63n(pow10(scaleDigits))
	return &RandomDecimal{fmt.Sprintf("%d.%0*d", integerPart, scaleDigits, fraction)}
}

// approximateDecimal keeps what float and double columns used to get, which is
// a small value rather than one filling the type.
func approximateDecimal(precision sql.NullInt64) float64 {
	f := rand.Float64()
	if precision.Valid && precision.Int64 > 0 {
		f *= float64(rand.Int63n(precision.Int64))
	}
	return f
}

func pow10(n int64) int64 {
	if n > maxDecimalDigits {
		n = maxDecimalDigits
	}
	p := int64(1)
	for i := int64(0); i < n; i++ {
		p *= 10
	}
	return p
}
