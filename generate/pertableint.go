package generate

import (
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/pkg/errors"
)

// splitPerTable walks the shared "N", "table=N", "N;table=N" form and hands
// each entry to set. Both separators are accepted: ";" is what these flags
// have always used, and a number holds no "," to confuse a reader with.
//
// set is called with everyTable as the key for the value that stands for the
// tables named nowhere else. A table cannot be called "", so the two slots
// cannot clash.
func splitPerTable(value string, malformed error, set func(key, number string) error) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == ',' }) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, number, perTable := strings.Cut(part, "=")
		if !perTable {
			if err := set(everyTable, part); err != nil {
				return err
			}
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return errors.Wrapf(malformed, "nothing named on the left of %q", part)
		}
		if err := set(strings.ToLower(key), strings.TrimSpace(number)); err != nil {
			return err
		}
	}
	return nil
}

// PerTableInt is PerTableFloat's counterpart for the counts that are whole
// numbers -- how many rows a table is filled with. Same shape and same
// separators, so a caller who has learnt one of these flags has learnt the
// others:
//
//	--rows=1000                            every table
//	--rows="orders=500000;products=20000"  per table
//	--rows="1000;orders=500000"            a default, and one exception
type PerTableInt map[string]int64

var ErrMalformedPerTableInt = errors.New(`malformed value, expected a whole number, "table=number", or several of those separated by ";". Example: --rows="1000;orders=500000;order_items=1500000"`)

func (r *PerTableInt) Decode(ctx *kong.DecodeContext, target reflect.Value) error {
	var value string
	if err := ctx.Scan.PopValueInto("value", &value); err != nil {
		return err
	}

	parsed, err := ParsePerTableInt(value)
	if err != nil {
		// kong prefixes the flag name itself
		return err
	}
	target.Set(reflect.ValueOf(parsed))
	return nil
}

// ParsePerTableInt reads the flag's value.
func ParsePerTableInt(value string) (PerTableInt, error) {
	parsed := PerTableInt{}
	err := splitPerTable(value, ErrMalformedPerTableInt, func(key, number string) error {
		if key == everyTable {
			if already, ok := parsed[everyTable]; ok {
				return errors.Wrapf(ErrMalformedPerTableInt, "two values given for every table, %d and %s", already, number)
			}
		}
		n, err := strconv.ParseInt(number, 10, 64)
		if err != nil {
			return errors.Wrap(ErrMalformedPerTableInt, err.Error())
		}
		parsed[key] = n
		return nil
	})
	return parsed, err
}

// For returns the count given for this table, falling back to the one given
// for every table, and 0 when neither was.
func (r PerTableInt) For(table string) int64 {
	if v, ok := r[strings.ToLower(table)]; ok {
		return v
	}
	return r[everyTable]
}

// IsSetFor reports whether a count was given for this table, so a caller can
// tell an explicit 0 from one nobody named.
func (r PerTableInt) IsSetFor(table string) bool {
	if _, ok := r[strings.ToLower(table)]; ok {
		return true
	}
	_, ok := r[everyTable]
	return ok
}

// Set records a count for one table, overriding whatever it held.
func (r PerTableInt) Set(table string, value int64) {
	r[strings.ToLower(table)] = value
}

// Tables returns the tables named explicitly, without the value standing for
// every table: naming a table's count is also naming a table to look at.
func (r PerTableInt) Tables() []string {
	names := make([]string, 0, len(r))
	for k := range r {
		if k != everyTable {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	return names
}

// Named returns only the counts given for a table by name, dropping the one
// that stands for every table. Callers that walk the counts to learn which
// tables were spoken about need this: the catch-all names no table, and
// walking it would invent one called "".
func (r PerTableInt) Named() map[string]int64 {
	named := make(map[string]int64, len(r))
	for k, v := range r {
		if k != everyTable {
			named[k] = v
		}
	}
	return named
}

// NewPerTableInt builds one holding a single value, for a caller that is not
// coming from the command line.
func NewPerTableInt(value int64) PerTableInt {
	return PerTableInt{everyTable: value}
}
