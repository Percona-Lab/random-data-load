package frequency

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// ColumnStats is one pg_stats row, reduced to the three figures this tool knows
// how to reproduce. The field names are pg_stats' own, so the dump query needs
// no column aliases and the file stays readable next to the catalog.
type ColumnStats struct {
	Schemaname string `json:"schemaname"`
	Tablename  string `json:"tablename"`
	Attname    string `json:"attname"`

	NullFrac float64 `json:"null_frac"`

	// Both arrays are indexed together: MostCommonVals[i] appears on
	// MostCommonFreqs[i] of the rows. postgres reports them separately and
	// either can be absent.
	MostCommonVals  []string  `json:"most_common_vals"`
	MostCommonFreqs []float64 `json:"most_common_freqs"`
}

// Resolver maps a dumped column onto a table and column of the current run,
// returning false when the dump mentions something this run does not touch. It
// exists so that this package does not need to know about the database
// catalog: the caller owns the loaded tables and settles naming and case.
type Resolver func(ColumnStats) (table, column string, ok bool)

var ErrMalformedStats = errors.New("malformed statistics export, expected the JSON array produced by the `export-stat` subcommand")

// LoadStats reads an export produced by the export-stat subcommand.
func LoadStats(path string) ([]ColumnStats, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.Wrap(err, "cannot read the statistics export")
	}
	defer f.Close()
	return ParseStats(f)
}

func ParseStats(r io.Reader) ([]ColumnStats, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, errors.Wrap(err, "cannot read the statistics export")
	}

	// psql prints nothing at all for an empty result set, and a lone "\N" when
	// the aggregate returned NULL. Neither means the dump is broken, they only
	// mean the columns asked for carry no statistics.
	stats := []ColumnStats{}
	if trimmed := strings.TrimSpace(string(raw)); trimmed == "" || trimmed == `\N` {
		return stats, nil
	}
	if err := json.Unmarshal(raw, &stats); err != nil {
		return nil, errors.Wrap(ErrMalformedStats, err.Error())
	}
	return stats, nil
}

// MergeStats turns a pg_stats dump into null and value frequencies.
//
// Values already present for a column are left alone and are not duplicated:
// --null-freq-map, --values-freq-map and the literals taken from --query are
// deliberate, so they win over what the dump observed.
func MergeStats(stats []ColumnStats, resolve Resolver) {
	for _, cs := range stats {
		table, column, ok := resolve(cs)
		if !ok {
			log.Debug().Str("table", cs.Tablename).Str("column", cs.Attname).Msg("pg_stats dump mentions a column this run does not insert into, skipping")
			continue
		}

		colFreqMap, ok := SharedTableFrequency[table]
		if !ok {
			colFreqMap = map[string]Frequency{}
		}
		freq := colFreqMap[column]

		freq.mergeCommonValues(cs, table, column)
		freq.mergeNullFraction(cs, table, column)

		colFreqMap[column] = freq
		SharedTableFrequency[table] = colFreqMap
	}
}

// mergeCommonValues appends the most common values, keeping their observed
// frequencies as-is: InjectIndexValue already draws value i with probability
// MostCommonFreqs[i], which is exactly what pg_stats measured.
func (freq *Frequency) mergeCommonValues(cs ColumnStats, table, column string) {
	count := min(len(cs.MostCommonVals), len(cs.MostCommonFreqs))
	if count != len(cs.MostCommonVals) || count != len(cs.MostCommonFreqs) {
		log.Warn().Str("table", table).Str("column", column).
			Int("values", len(cs.MostCommonVals)).Int("frequencies", len(cs.MostCommonFreqs)).
			Msg("most_common_vals and most_common_freqs have different lengths in the dump, using the shorter one")
	}

	for i := 0; i < count; i++ {
		if cs.MostCommonFreqs[i] <= 0 {
			continue
		}
		if freq.claims(cs.MostCommonVals[i]) {
			log.Debug().Str("table", table).Str("column", column).Str("value", cs.MostCommonVals[i]).
				Msg("value already given a frequency on the command line or by the query, keeping that one")
			continue
		}
		freq.IndexValues = append(freq.IndexValues, cs.MostCommonVals[i])
		freq.IndexFrequencies = append(freq.IndexFrequencies, cs.MostCommonFreqs[i])
	}
}

// claims reports whether the value is already going to be inserted. An entry
// left at a frequency of zero claims nothing: it would never be inserted, so
// treating it as a decision would only silence the dump.
func (freq *Frequency) claims(value string) bool {
	for i, existing := range freq.IndexValues {
		if existing == value && i < len(freq.IndexFrequencies) && freq.IndexFrequencies[i] > 0 {
			return true
		}
	}
	return false
}

// mergeNullFraction scales null_frac up before storing it.
//
// A row is drawn as NULL first and then overwritten when an index value is
// drawn, so a column carrying index values on a total of T of its rows only
// keeps its NULLs on the remaining 1-T. Dividing here is what makes the
// generated table end up with the null_frac that was measured.
func (freq *Frequency) mergeNullFraction(cs ColumnStats, table, column string) {
	if freq.nullFromFlag {
		log.Debug().Str("table", table).Str("column", column).Msg("--null-freq-map set this column explicitly, ignoring null_frac from the dump")
		return
	}

	var indexed float64
	for _, f := range freq.IndexFrequencies {
		indexed += f
	}
	if indexed >= 1 {
		log.Warn().Str("table", table).Str("column", column).Float64("total", indexed).
			Msg("values already cover every row of this column, its null_frac cannot be reproduced")
		freq.Null = 0
		return
	}

	freq.Null = min(cs.NullFrac/(1-indexed), 1)
}
