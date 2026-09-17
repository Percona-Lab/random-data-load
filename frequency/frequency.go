package frequency

import (
	"math/rand"
	"reflect"
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

	// KeyFrequencies is how often a foreign key column repeats its most common
	// values, without the values themselves. The values a dump holds for such
	// a column are the source database's own parent ids and mean nothing here,
	// but how skewed the column is means a great deal: postgres sizes a hash
	// join's build side from the most common frequency of the join column.
	// Reproduced by sampling the parent's rows in those proportions.
	KeyFrequencies []float64

	// Set when the entry came from a command line flag rather than from a
	// scanned pg_stats dump, so that an explicit flag is never overwritten by
	// what the dump happens to say.
	nullFromFlag bool
}

// SetNullFromFlag records the null fraction --null-freq named for one column,
// marking it as given on the command line so that a statistics dump read
// later leaves it alone.
func SetNullFromFlag(table, column string, freq float64) {
	colMap, ok := SharedTableFrequency[table]
	if !ok {
		colMap = map[string]Frequency{}
	}
	stored := colMap[column]
	stored.Null = freq
	stored.nullFromFlag = true
	colMap[column] = stored
	SharedTableFrequency[table] = colMap
}

func init() {
	SharedTableFrequency = map[string]ColumnFrequency{}
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

// MergeQueryParameters pins the literals a --query compares a column to, so
// that the column actually holds some of them.
//
// This is an instruction, not a measurement. The frequency is whatever
// --query-param-freq was set to, which is a number nobody observed, so
// everything merged afterwards leaves those values alone -- including an
// imported export, which is the point: the caller asking for a literal at a
// rate is overriding what the export says about it.
//
// Nothing is pinned unless --query-param-freq is set, and it defaults to 0. A
// selectivity nobody measured is still a selectivity, and a run that quietly
// applies one produces a plan that is wrong in a way no row count shows.
//
// A value already given a frequency is left alone rather than pinned a second
// time: the two entries used to be drawn independently and add up, so pinning
// a value --values-freq-map also names overshot it.
func MergeQueryParameters(params map[string][]string, defaultFrequency float64) {
	if defaultFrequency <= 0 {
		return
	}
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
		for _, value := range values {
			if freq.claims(value) {
				log.Debug().Str("table", parts[0]).Str("column", parts[1]).Str("value", value).
					Msg("value already given a frequency on the command line, keeping that one instead of adding the query's")
				continue
			}
			freq.IndexValues = append(freq.IndexValues, value)
			freq.IndexFrequencies = append(freq.IndexFrequencies, defaultFrequency)
		}
		colFreqMap[parts[1]] = freq
		SharedTableFrequency[parts[0]] = colFreqMap
	}

}

// WillInsert reports whether anything is going to put this value in this
// column: a --values-freq-map entry, a query literal pinned by
// --query-param-freq, or an imported export listing it among the column's most
// common values.
//
// A query whose predicate matches nothing returns no rows, and the caller has
// to be told which predicate that was rather than left to work it out from an
// empty result. Names are matched case-insensitively, because the query's
// spelling and the catalog's need not agree.
func WillInsert(table, column, value string) bool {
	for haveTable, columns := range SharedTableFrequency {
		if !strings.EqualFold(haveTable, table) {
			continue
		}
		for haveColumn, freq := range columns {
			if strings.EqualFold(haveColumn, column) && freq.claims(value) {
				return true
			}
		}
	}
	return false
}
