package generate

import (
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/pkg/errors"
)

// everyTable is the key holding the value given for the tables that have no
// entry of their own. A table cannot be named "", so it cannot clash.
const everyTable = ""

// PerTableFloat is a number given once for the whole run, or separately for
// the tables that need their own.
//
// What these flags should be set to depends on the table. A sampler is tuned
// by the parent it reads: how low a coin flip can go is decided by that
// parent's row count, and the mean of a normal law is a row number inside it.
// A row-width target is the same shape the other way round -- it describes the
// table being filled -- and both leave one number for the whole run unable to
// satisfy a run touching several tables. Splitting it into one invocation per
// table was the way around that, which also meant working the value out again
// for each one and a much longer script to hand over afterwards.
//
//	--coin-flip-percent=3                       every relationship
//	--coin-flip-percent="orders=3;products=5"   per parent table
//	--coin-flip-percent="1;orders=3"            a default, and one exception
type PerTableFloat map[string]float64

var ErrMalformedPerTableFloat = errors.New(`malformed value, expected a number, "table=number", or several of those separated by ";". Example: --coin-flip-percent="1;orders=3;products=5"`)

func (r *PerTableFloat) Decode(ctx *kong.DecodeContext, target reflect.Value) error {
	var value string
	if err := ctx.Scan.PopValueInto("value", &value); err != nil {
		return err
	}

	parsed, err := ParsePerTableFloat(value)
	if err != nil {
		// kong prefixes the flag name itself
		return err
	}
	target.Set(reflect.ValueOf(parsed))
	return nil
}

// ParsePerTableFloat reads the flag's value. Both separators are accepted:
// ";" is what --rows-per-table uses, and a number holds no "," to confuse a
// reader with.
func ParsePerTableFloat(value string) (PerTableFloat, error) {
	parsed := PerTableFloat{}
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
			if _, already := parsed[everyTable]; already {
				return parsed, errors.Wrapf(ErrMalformedPerTableFloat, "two values given for every relationship, %g and %s", parsed[everyTable], part)
			}
			f, err := strconv.ParseFloat(part, 64)
			if err != nil {
				return parsed, errors.Wrap(ErrMalformedPerTableFloat, err.Error())
			}
			parsed[everyTable] = f
			continue
		}

		table = strings.TrimSpace(table)
		if table == "" {
			return parsed, errors.Wrapf(ErrMalformedPerTableFloat, "no table named in %q", part)
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
		if err != nil {
			return parsed, errors.Wrap(ErrMalformedPerTableFloat, err.Error())
		}
		parsed[strings.ToLower(table)] = f
	}
	return parsed, nil
}

// For returns the value given for this table, which is 0 when none was given
// and the caller works one out for itself.
func (r PerTableFloat) For(parent string) float64 {
	if v, ok := r[strings.ToLower(parent)]; ok {
		return v
	}
	return r[everyTable]
}

// IsSetFor reports whether a value was given for this table, so a caller can
// tell an explicit 0 from one it should work out itself.
func (r PerTableFloat) IsSetFor(parent string) bool {
	if _, ok := r[strings.ToLower(parent)]; ok {
		return true
	}
	_, ok := r[everyTable]
	return ok
}

// Values returns every value given, for a flag whose range has to be checked
// before any sampling starts.
func (r PerTableFloat) Values() []float64 {
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

// NewPerTableFloat builds one holding a single value, for a caller that is
// not coming from the command line.
func NewPerTableFloat(value float64) PerTableFloat {
	return PerTableFloat{everyTable: value}
}
