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

// Target is where a dumped column lands in the current run.
type Target struct {
	Table, Column string

	// ForeignKey marks a column whose values are sampled from a parent rather
	// than generated. What the dump holds for it cannot be reused as-is.
	ForeignKey bool
}

// Resolver maps a dumped column onto a column of the current run, returning
// false when the dump mentions something this run does not touch. It exists so
// that this package does not need to know about the database catalog: the
// caller owns the loaded tables and settles naming, case, and which columns
// are keys.
type Resolver func(ColumnStats) (Target, bool)

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
// --null-freq, --values-freq-map and the literals taken from --query are
// deliberate, so they win over what the dump observed.
func MergeStats(stats []ColumnStats, resolve Resolver) {
	for _, cs := range stats {
		target, ok := resolve(cs)
		if !ok {
			log.Debug().Str("table", cs.Tablename).Str("column", cs.Attname).Msg("pg_stats dump mentions a column this run does not insert into, skipping")
			continue
		}

		colFreqMap, ok := SharedTableFrequency[target.Table]
		if !ok {
			colFreqMap = map[string]Frequency{}
		}
		freq := colFreqMap[target.Column]

		if target.ForeignKey {
			freq.keepKeySkew(cs, target)
		} else {
			freq.mergeCommonValues(cs, target.Table, target.Column)
		}
		freq.mergeNullFraction(cs, target.Table, target.Column)

		colFreqMap[target.Column] = freq
		SharedTableFrequency[target.Table] = colFreqMap
	}
}

// keepKeySkew keeps how skewed a foreign key column is, and drops the values it
// was skewed towards.
//
// The dump matches by table and column name, so it happily hands over the
// most_common_vals of a foreign key: real parent ids from the source database.
// Inserting them here points the key at rows that do not exist, since the local
// parent was filled with ids of this run's own making, and every run of the
// study had to strip those entries from the dump by hand or watch the sampling
// fail.
//
// The frequencies are worth keeping though, and are the hardest thing in a
// reproduction to get right: postgres reads most_common_freqs[1] of the inner
// join column to size a hash join's build side, so join-key skew is what
// decides which side of the join gets built. It is reproduced by sampling that
// proportion of the child's rows from one parent row each, which needs no ids
// to be invented.
func (freq *Frequency) keepKeySkew(cs ColumnStats, target Target) {
	count := min(len(cs.MostCommonVals), len(cs.MostCommonFreqs))
	if count == 0 {
		return
	}

	kept := make([]float64, 0, count)
	for i := 0; i < count; i++ {
		if cs.MostCommonFreqs[i] > 0 {
			kept = append(kept, cs.MostCommonFreqs[i])
		}
	}
	if len(kept) == 0 {
		return
	}

	freq.KeyFrequencies = kept
	log.Info().Str("table", target.Table).Str("column", target.Column).Int("values", len(kept)).Float64("mostCommon", kept[0]).
		Msgf("%s.%s is a foreign key, so the values the dump holds for it are the source database's own parent ids and cannot be inserted here. Keeping how often they repeat -- %d values, the most common on %.4f of the rows -- and reproducing it by sampling that share of the rows from one parent row each",
			target.Table, target.Column, len(kept), kept[0])
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
		log.Debug().Str("table", table).Str("column", column).Msg("--null-freq set this column explicitly, ignoring null_frac from the dump")
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
