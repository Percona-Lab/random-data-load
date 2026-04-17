package frequency

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/pkg/errors"
)

type TableFrequency map[string]ColumnFrequency

type ColumnFrequency map[string]Frequency

type Frequency struct {
	Null int
}

type FrequencyNullParameter TableFrequency

var ErrMalformedFrequencyNullParameter = errors.New("malformed null frequency mapping, the format is \"table.col=(0-100)[;table.col2=(0-100)]\"")

func (fnp *FrequencyNullParameter) Decode(ctx *kong.DecodeContext, target reflect.Value) error {
	var value string
	err := ctx.Scan.PopValueInto("value", &value)
	if err != nil {
		return err
	}

	tableMap := map[string]ColumnFrequency{}
	colMap := map[string]Frequency{}

	args := strings.Split(value, ";")
	for _, arg := range args {

		parts := strings.Split(arg, "=")
		if len(parts) != 2 {
			return ErrMalformedFrequencyNullParameter
		}

		tableColParts := strings.Split(parts[0], ".")
		if len(tableColParts) != 2 {
			return ErrMalformedFrequencyNullParameter
		}

		freq, err := strconv.Atoi(parts[1])
		if err != nil {
			return errors.Wrap(ErrMalformedFrequencyNullParameter, err.Error())
		}

		var ok bool
		if colMap, ok = tableMap[tableColParts[0]]; !ok {
			colMap = map[string]Frequency{}
		}
		colMap[tableColParts[1]] = Frequency{
			Null: freq,
		}
		tableMap[tableColParts[0]] = colMap
	}

	target.Set(reflect.ValueOf(tableMap))
	return nil
}

var DefaultNullFrequency = 10

func (c ColumnFrequency) NullForColumn(col string, isNullable bool) int {
	if !isNullable {
		return 0
	}
	if freq, ok := c[col]; ok {
		return freq.Null
	}
	return DefaultNullFrequency
}
