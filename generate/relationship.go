package generate

import (
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/pkg/errors"
)

// everyRelationship is the key holding the value given for relationships that
// have no entry of their own. A table cannot be named "", so it cannot clash.
const everyRelationship = ""

// RelationshipFloat is a number given once for every relationship, or
// separately for the ones that need their own. It is keyed by parent table:
// the side being sampled, which is what the value describes.
//
// What these flags should be set to depends on that parent. How low a coin
// flip can go is decided by its row count, and the mean of a normal law is a
// row number inside it, so one number for the whole run cannot satisfy a run
// touching several relationships. Splitting it into one invocation per table
// was the way around that, which also meant working the value out again for
// each one and a much longer script to hand over afterwards.
//
//	--coin-flip-percent=3                       every relationship
//	--coin-flip-percent="orders=3;products=5"   per parent table
//	--coin-flip-percent="1;orders=3"            a default, and one exception
type RelationshipFloat map[string]float64

var ErrMalformedRelationshipFloat = errors.New(`malformed value, expected a number, "table=number", or several of those separated by ";". Example: --coin-flip-percent="1;orders=3;products=5"`)

func (r *RelationshipFloat) Decode(ctx *kong.DecodeContext, target reflect.Value) error {
	var value string
	if err := ctx.Scan.PopValueInto("value", &value); err != nil {
		return err
	}

	parsed, err := ParseRelationshipFloat(value)
	if err != nil {
		// kong prefixes the flag name itself
		return err
	}
	target.Set(reflect.ValueOf(parsed))
	return nil
}

// ParseRelationshipFloat reads the flag's value. Both separators are accepted:
// ";" is what --rows-per-table uses, and a number holds no "," to confuse a
// reader with.
func ParseRelationshipFloat(value string) (RelationshipFloat, error) {
	parsed := RelationshipFloat{}
	if strings.TrimSpace(value) == "" {
		return parsed, nil
	}

	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == ',' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		table, number, perTable := strings.Cut(part, "=")
		if !perTable {
			if _, already := parsed[everyRelationship]; already {
				return parsed, errors.Wrapf(ErrMalformedRelationshipFloat, "two values given for every relationship, %g and %s", parsed[everyRelationship], part)
			}
			f, err := strconv.ParseFloat(part, 64)
			if err != nil {
				return parsed, errors.Wrap(ErrMalformedRelationshipFloat, err.Error())
			}
			parsed[everyRelationship] = f
			continue
		}

		table = strings.TrimSpace(table)
		if table == "" {
			return parsed, errors.Wrapf(ErrMalformedRelationshipFloat, "no table named in %q", part)
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
		if err != nil {
			return parsed, errors.Wrap(ErrMalformedRelationshipFloat, err.Error())
		}
		parsed[strings.ToLower(table)] = f
	}
	return parsed, nil
}

// For returns the value this parent should be sampled with, which is 0 when
// none was given and the sampler works one out from the parent itself.
func (r RelationshipFloat) For(parent string) float64 {
	if v, ok := r[strings.ToLower(parent)]; ok {
		return v
	}
	return r[everyRelationship]
}

// IsSetFor reports whether a value was given for this parent, so a sampler can
// tell an explicit 0 from one it should work out itself.
func (r RelationshipFloat) IsSetFor(parent string) bool {
	if _, ok := r[strings.ToLower(parent)]; ok {
		return true
	}
	_, ok := r[everyRelationship]
	return ok
}

// Values returns every value given, for a flag whose range has to be checked
// before any sampling starts.
func (r RelationshipFloat) Values() []float64 {
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	values := make([]float64, 0, len(r))
	for _, k := range keys {
		values = append(values, r[k])
	}
	return values
}

// NewRelationshipFloat builds one holding a single value, for a caller that is
// not coming from the command line.
func NewRelationshipFloat(value float64) RelationshipFloat {
	return RelationshipFloat{everyRelationship: value}
}
