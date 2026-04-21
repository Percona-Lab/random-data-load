package frequency

import (
	"math/rand"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

type TableFrequency map[string]ColumnFrequency

var SharedTableFrequency map[string]ColumnFrequency // to merge parameters into a single array

type ColumnFrequency map[string]Frequency

type Frequency struct {
	Null             float64
	IndexValues      []string  // list of values that should  end up in the column
	IndexFrequencies []float64 // with their associated frequencies
}

type FrequencyNullParameter TableFrequency

var ErrMalformedFrequencyNullParameter = errors.New("malformed null frequency mapping, the format is \"table.col=(0.0-1.0)[;table.col2=(0.0-1.0)]\". Example \nitems.tags=0.63;items.price=0\"")

func init() {
	SharedTableFrequency = map[string]ColumnFrequency{}
}

func (fnp *FrequencyNullParameter) Decode(ctx *kong.DecodeContext, target reflect.Value) error {
	var value string
	err := ctx.Scan.PopValueInto("value", &value)
	if err != nil {
		return err
	}

	colMap := map[string]Frequency{}

	args := strings.Split(value, ";")
	if len(value) == 0 {
		goto AFFECT_NONETHELESS
	}
	for _, arg := range args {

		parts := strings.Split(arg, "=")
		if len(parts) != 2 {
			return ErrMalformedFrequencyNullParameter
		}

		tableColParts := strings.Split(parts[0], ".")
		if len(tableColParts) != 2 {
			return ErrMalformedFrequencyNullParameter
		}

		freq, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return errors.Wrap(ErrMalformedFrequencyNullParameter, err.Error())
		}

		var ok bool
		if colMap, ok = SharedTableFrequency[tableColParts[0]]; !ok {
			colMap = map[string]Frequency{}
		}
		colMap[tableColParts[1]] = Frequency{
			Null: freq,
		}
		SharedTableFrequency[tableColParts[0]] = colMap
	}

AFFECT_NONETHELESS:
	target.Set(reflect.ValueOf(SharedTableFrequency))
	return nil
}

var DefaultNullFrequency = 0.1

func (c ColumnFrequency) Null(col string, isNullable bool) bool {
	if !isNullable {
		return false
	}
	nullFreq := DefaultNullFrequency
	if colFreq, ok := c[col]; ok {
		nullFreq = colFreq.Null
	}
	return nullFreq > 0 && rand.Float64() < nullFreq
}

type FrequencyIndexValuesParameter TableFrequency

var ErrMalformedFrequencyIndexValuesParameter = errors.New("malformed values frequency mapping, the format is \"table.col=val:(0.0-1.0),val2(0.0-1.0)[;table.col2=(0.0-1.0)]\". Example \nitems.tags=web:0.925,cloud:0.17;items.price=9.99:0.59\"")

func (fnp *FrequencyIndexValuesParameter) Decode(ctx *kong.DecodeContext, target reflect.Value) error {
	var value string
	err := ctx.Scan.PopValueInto("value", &value)
	if err != nil {
		return err
	}

	colMap := map[string]Frequency{}

	args := strings.Split(value, ";")
	if len(value) == 0 {
		goto AFFECT_NONETHELESS
	}
	for _, arg := range args {

		parts := strings.Split(arg, "=")
		if len(parts) != 2 {
			return errors.Wrap(ErrMalformedFrequencyIndexValuesParameter, "missing =, or too many =")
		}

		tableColParts := strings.Split(parts[0], ".")
		if len(tableColParts) != 2 {
			return errors.Wrapf(ErrMalformedFrequencyIndexValuesParameter, "malformed table.col: %s", parts[0])
		}

		valParts := strings.Split(parts[1], ",")
		for _, val := range valParts {
			valFreqParts := strings.Split(val, ":")
			if len(valFreqParts) != 2 {
				return errors.Wrapf(ErrMalformedFrequencyIndexValuesParameter, "malformed 'val:freq': %s", parts[0])
			}

			freq, err := strconv.ParseFloat(valFreqParts[1], 64)
			if err != nil {
				return errors.Wrap(ErrMalformedFrequencyIndexValuesParameter, err.Error())
			}

			var ok bool
			if colMap, ok = SharedTableFrequency[tableColParts[0]]; !ok {
				colMap = map[string]Frequency{}
			}
			storedFreq := colMap[tableColParts[1]]
			storedFreq.IndexValues = append(storedFreq.IndexValues, valFreqParts[0])
			storedFreq.IndexFrequencies = append(storedFreq.IndexFrequencies, freq)
			colMap[tableColParts[1]] = storedFreq
			SharedTableFrequency[tableColParts[0]] = colMap
		}
	}

AFFECT_NONETHELESS:
	target.Set(reflect.ValueOf(SharedTableFrequency))
	return nil
}

func (c ColumnFrequency) InjectIndexValue(col string) (string, bool) {
	colFreq, ok := c[col]
	if !ok {
		return "", false
	}

	totalFreq := 1.0
	for i, idxFreq := range colFreq.IndexFrequencies {
		randFloat := rand.Float64()

		// without dividing by total freq, the end repartition would not respect the freq
		// example: with val1:0.37 and val2:0.34, if we do not divide the frequency by (1.0 - 0.37), val2
		// would only be present on --rows*0.37*0.34, instead of --rows*0.34.
		if randFloat < idxFreq/totalFreq {
			return colFreq.IndexValues[i], true
		}
		totalFreq -= idxFreq
	}

	return "", false
}

func MergeQueryParameters(params map[string][]string, defaultFrequency float64) {
	for tableCol, values := range params {
		parts := strings.Split(tableCol, ".")
		if len(parts) != 2 {
			log.Debug().Str("queryParams idx", tableCol).Msg("queryParams malformed")
			continue
		}
		colFreqMap, ok := SharedTableFrequency[parts[0]]
		if !ok {
			colFreqMap = map[string]Frequency{}
		}
		freq, ok := colFreqMap[parts[1]]
		if !ok {
			freq = Frequency{}
		}
		freq.IndexValues = append(freq.IndexValues, values...)
		freq.IndexFrequencies = append(freq.IndexFrequencies, slices.Repeat([]float64{defaultFrequency}, len(values))...)
		colFreqMap[parts[1]] = freq
		SharedTableFrequency[parts[0]] = colFreqMap
	}

}
