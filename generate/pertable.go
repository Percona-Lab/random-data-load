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
// ";" is what these flags use, and a number holds no "," to confuse a reader
// with.
func ParsePerTableFloat(value string) (PerTableFloat, error) {
	parsed := PerTableFloat{}
	err := splitPerTable(value, ErrMalformedPerTableFloat, func(key, number string) error {
		if key == everyTable {
			if already, ok := parsed[everyTable]; ok {
				return errors.Wrapf(ErrMalformedPerTableFloat, "two values given for every relationship, %g and %s", already, number)
			}
		}
		f, err := strconv.ParseFloat(number, 64)
		if err != nil {
			return errors.Wrap(ErrMalformedPerTableFloat, err.Error())
		}
		parsed[key] = f
		return nil
	})
	return parsed, err
}

// ForColumn returns the value given for one column, for the flags whose
// subject is a column rather than a table -- a null fraction belongs to a
// column, since a table is not the thing that can be NULL. Such a flag is
// written "table.column=0.63", and the bare number still stands for
// everything the run touches.
func (r PerTableFloat) ForColumn(table, column string) float64 {
	if v, ok := r[strings.ToLower(table+"."+column)]; ok {
		return v
	}
	return r[everyTable]
}

// IsSetForColumn reports whether this exact column was named.
func (r PerTableFloat) IsSetForColumn(table, column string) bool {
	_, ok := r[strings.ToLower(table+"."+column)]
	return ok
}

// Columns returns the "table.column" keys that named a column, so a caller
// can check their shape and fan them out. Anything that is not a bare number
// and holds no "." is returned too, for the caller to reject by name.
func (r PerTableFloat) Columns() []string {
	keys := make([]string, 0, len(r))
	for k := range r {
		if k != everyTable {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
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
