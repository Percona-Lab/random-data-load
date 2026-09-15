package generate

import (
	"math/rand"
	"regexp"
	"strings"

	"github.com/brianvoe/gofakeit/v7"
)

// RandomString getter
type RandomString struct {
	value string
}

var (
	emailRe     = regexp.MustCompile(`email`)
	firstNameRe = regexp.MustCompile(`first.*name`)
	lastNameRe  = regexp.MustCompile(`last.*name`)
	nameRe      = regexp.MustCompile(`name`)
	phoneRe     = regexp.MustCompile(`phone`)
	ssn         = regexp.MustCompile(`ssn`)
	zipRe       = regexp.MustCompile(`zip`)
	colorRe     = regexp.MustCompile(`color`)
	ipAddressRe = regexp.MustCompile(`^ip.*(?:address)*`)
	addressRe   = regexp.MustCompile(`address`)
	stateRe     = regexp.MustCompile(`state`)
	cityRe      = regexp.MustCompile(`city`)
	countryRe   = regexp.MustCompile(`country`)
	genderRe    = regexp.MustCompile(`gender`)
	urlRe       = regexp.MustCompile(`url`)
	domainre    = regexp.MustCompile(`domain`)
	productName = regexp.MustCompile(`product`)
	description = regexp.MustCompile(`description`)
	feature     = regexp.MustCompile(`feature`)
	material    = regexp.MustCompile(`material`)
	currency    = regexp.MustCompile(`currency`)
	company     = regexp.MustCompile(`company`)
	language    = regexp.MustCompile(`language`)
)

func (r *RandomString) String() string {
	return r.value
}

func (r *RandomString) IsQuotable() bool {
	return true
}

func NewRandomString(name string, maxSize int64) *RandomString {

	name = strings.ToLower(name)

	var fn func() string

	switch {
	case emailRe.MatchString(name):
		fn = gofakeit.Email
	case firstNameRe.MatchString(name):
		fn = gofakeit.FirstName
	case lastNameRe.MatchString(name):
		fn = gofakeit.LastName
	case nameRe.MatchString(name):
		fn = gofakeit.Name
	case phoneRe.MatchString(name):
		fn = gofakeit.PhoneFormatted
	case ssn.MatchString(name):
		fn = gofakeit.SSN
	case zipRe.MatchString(name):
		fn = gofakeit.Zip
	case colorRe.MatchString(name):
		fn = gofakeit.Color
	case cityRe.MatchString(name):
		fn = gofakeit.City
	case countryRe.MatchString(name):
		fn = gofakeit.Country
		if maxSize > 0 && maxSize < 4 {
			fn = gofakeit.CountryAbr
		}
	case ipAddressRe.MatchString(name):
		fn = gofakeit.IPv4Address
	case addressRe.MatchString(name):
		fn = gofakeit.Street
	case productName.MatchString(name):
		fn = gofakeit.ProductName
	case description.MatchString(name):
		fn = gofakeit.ProductDescription
	case feature.MatchString(name):
		fn = gofakeit.ProductFeature
	case material.MatchString(name):
		fn = gofakeit.ProductMaterial
	case currency.MatchString(name):
		fn = gofakeit.CurrencyShort
	case company.MatchString(name):
		fn = gofakeit.Company
	case language.MatchString(name):
		fn = gofakeit.Language
	default:
		fn = func() string {
			return gofakeit.ID()
		}
	}

	s := truncateRunes(fn(), maxSize)
	// quick and dirty fix to avoid breaking sql
	// using ? placeholders would be better
	s = strings.Replace(s, "'", "", -1)
	return &RandomString{s}
}

// truncateRunes cuts a string down to max characters without splitting a
// multi-byte one.
//
// Slicing by byte does split one, and the database refuses the whole batch
// rather than the row: a country name cut to the two bytes a char(2) column
// holds ends halfway through an "Å", and postgres answers "invalid byte
// sequence for encoding UTF8". Column widths count characters on both engines,
// so characters is also the right unit to cut on.
func truncateRunes(s string, max int64) string {
	if max <= 0 || int64(len(s)) <= max {
		return s
	}
	count := int64(0)
	for i := range s {
		if count == max {
			return s[:i]
		}
		count++
	}
	return s
}

// fillerAlphabet is what a value is padded with. Letters and digits only: the
// padding lands in a text column of a real schema, it goes through an INSERT
// as a literal, and it should not be the reason a run fails.
const fillerAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// NewFilledString returns a value of the column's usual shape, written out to
// a given length.
//
// Row width decides how many rows fit in a page, so hitting a page count means
// hitting a row width, and the only columns whose width this tool can choose
// are the ones holding free text. The value still starts as whatever the
// column's name suggests -- an email column still holds an email -- and is
// then padded so that the column takes the room it was asked to take.
//
// The padding is random rather than repeated, because postgres compresses a
// varlena before deciding whether to push it out of line, and a column padded
// with one repeated character compresses to nothing and takes no room at all.
//
// maxLength is the column's own limit, in characters. A target that does not
// fit in the column is cut down to what does: the calibration pass has already
// warned about the width it could not reach, and writing a value the column
// refuses would fail the whole bulk.
func NewFilledString(name string, length, maxLength int64) *RandomString {
	if maxLength > 0 && length > maxLength {
		length = maxLength
	}
	if length < 0 {
		length = 0
	}

	value := NewRandomString(name, length).value

	// The base value is counted in bytes, the column's limit in characters, so
	// both are watched: a name holding an accent is two bytes and one
	// character, and padding to the byte target would overrun a char(n).
	characters := int64(len([]rune(value)))
	var b strings.Builder
	b.WriteString(value)
	for int64(b.Len()) < length && (maxLength <= 0 || characters < maxLength) {
		b.WriteByte(fillerAlphabet[rand.Intn(len(fillerAlphabet))])
		characters++
	}
	return &RandomString{truncateRunes(b.String(), maxLength)}
}
