package getters

import (
	"fmt"
	"math/rand"
	"regexp"

	"github.com/brianvoe/gofakeit"
)

// RandomString getter
type RandomString struct {
	value string
	null  bool
}

var (
	emailRe     = regexp.MustCompile(`email`)
	firstNameRe = regexp.MustCompile(`first.*name`)
	lastNameRe  = regexp.MustCompile(`last.*name`)
	nameRe      = regexp.MustCompile(`name`)
	phoneRe     = regexp.MustCompile(`phone`)
	zipRe       = regexp.MustCompile(`zip`)
	colorRe     = regexp.MustCompile(`color`)
	ipAddressRe = regexp.MustCompile(`ip.*(?:address)*`)
	addressRe   = regexp.MustCompile(`address`)
	stateRe     = regexp.MustCompile(`state`)
	cityRe      = regexp.MustCompile(`city`)
	countryRe   = regexp.MustCompile(`country`)
	genderRe    = regexp.MustCompile(`gender`)
	urlRe       = regexp.MustCompile(`url`)
	domainre    = regexp.MustCompile(`domain`)
)

func (r *RandomString) Value() interface{} {
	return r.value
}

func (r *RandomString) String() string {
	if r.null {
		return NULL
	}
	return r.value
}

// Quote returns a quoted string
func (r *RandomString) Quote() string {
	if r.null {
		return NULL
	}
	return fmt.Sprintf("'%s'", r.value)
}

func NewRandomString(name string, maxSize int64, allowNull bool) *RandomString {

	if allowNull && rand.Int63n(100) < nilFrequency {
		return &RandomString{"", true}
	}
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
	case zipRe.MatchString(name):
		fn = gofakeit.Zip
	case colorRe.MatchString(name):
		fn = gofakeit.Color
	case cityRe.MatchString(name):
		fn = gofakeit.City
	case countryRe.MatchString(name):
		fn = gofakeit.Country
	case addressRe.MatchString(name):
		fn = gofakeit.Street
	case ipAddressRe.MatchString(name):
		fn = gofakeit.IPv4Address
	default:
		fn = func() string {
			return gofakeit.Paragraph(10, 10, 10, " ")
		}
	}

	s := fn()
	if len(s) > int(maxSize) {
		s = s[:int(maxSize)]
	}
	return &RandomString{s, false}
}
