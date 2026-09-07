package generate

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Percona-Lab/random-data-load/db"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// maxNormalDraws caps how many row numbers the normal law is asked for before
// the table's closest row is taken instead.
const maxNormalDraws = 1000

// minBernoulliCandidates is how many rows a single coin-flip sample has to be
// expected to bring back before --coin-flip-percent is left alone. A draw
// expected to return this many rows misses every one of them about once in
// 10^9 samples; the default percentage over a small parent misses them often,
// and an empty sample fills no foreign key.
const minBernoulliCandidates = 20

type Sampler interface {
	Sample() error
}

type SamplerBuilder func([]db.Field, string, string, string, [][]Getter, int64, *ForeignKeyLinks) Sampler

type sampleCommon struct {
	schema         string
	table          string
	constraintName string
	fields         []db.Field
	values         [][]Getter
	limit          int
	tableSize      int64
	fkCli          *ForeignKeyLinks
}

func (s *sampleCommon) query(query string, values [][]Getter) error {

	log.Debug().Str("query", query).Str("tablename", s.table).Str("schema", s.schema).Msg("query")
	rows, err := db.DB.Query(query)
	if err != nil {
		return fmt.Errorf("cannot get samples: %s, %s", query, err)
	}
	defer rows.Close()

	var rowIdx int
	for rows.Next() {

		scannedValuesInterface := make([]interface{}, len(s.fields))
		scannedGetter := make([]ScannerGetter, len(s.fields))
		for fieldIdx, field := range s.fields {
			getter := s.getterFromField(field)
			if getter == nil {
				// Unreachable: getterFromField falls back on a string reader.
				// Left in because a nil destination reaching rows.Scan used to
				// be a segmentation fault rather than an error.
				return errors.Errorf("no way to read column %s.%s of type %s, needed to fill a foreign key", s.table, field.ColumnName, field.DataType)
			}
			scannedGetter[fieldIdx] = getter
			scannedValuesInterface[fieldIdx] = getter
		}
		err = rows.Scan(scannedValuesInterface...)
		if err != nil {
			return errors.Wrapf(err, "cannot read the sampled rows of %s.%s (columns %s), a parent key type may not be readable, query %s",
				s.schema, s.table, db.EscapedNamesListFromFields(s.fields), query)
		}
		for fieldIdx := range s.fields {
			values[rowIdx][fieldIdx] = &GetterWrapper{scannedGetter[fieldIdx]}
		}

		rowIdx = rowIdx + 1

		if rowIdx == len(values) {
			err = rows.Close()
			if err != nil {
				return errors.Wrap(err, "cannot close rows while sampling")
			}
		}
	}

	if rowIdx == 0 {
		return errors.Errorf("sampling %s.%s brought back no row at all, so the foreign key of this insert cannot be filled. Query: %s",
			s.schema, s.table, query)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("cannot get samples: %s", err)
	}
	if rowIdx < len(values) {
		log.Debug().Str("query", query).Str("tablename", s.table).Str("schema", s.schema).Int("rowIdx", rowIdx).Int("len(values)", len(values)).Msg("looping again because we lacked samples")
		return s.query(query, values[rowIdx:])
	}
	return nil
}

// getterFromField picks how to read one column of a parent row.
//
// The list is wider than the one the generator uses: a column this tool cannot
// generate can still be the key of a table it has to point at, and a uuid or a
// numeric primary key is a common one. Anything unlisted is read as text,
// which is what a driver hands over for a type it has nothing better for; if
// it hands over something else instead, the scan says so, naming the column.
func (s *sampleCommon) getterFromField(f db.Field) ScannerGetter {

	switch f.DataType {
	case "tinyint", "smallint", "mediumint", "int", "integer", "bigint", "year":
		return NewScannedInt()
	case "char", "varchar", "blob", "text", "tinytext", "tinyblob", "mediumtext",
		"mediumblob", "longblob", "longtext", "uuid", "enum", "set":
		return NewScannedString()
	case "binary", "varbinary":
		return NewScannedBinary()
	case "float", "decimal", "double", "numeric":
		// postgres reports both numeric(p,s) and decimal(p,s) as numeric, and
		// hands the value over as text either way.
		return NewScannedDecimal()
	case "bool", "boolean":
		return NewScannedBool()
	case "date", "time", "datetime", "timestamp":
		return NewScannedTime()
	}
	logOnce("unlistedSampledType:"+s.schema+"."+s.table+"."+f.ColumnName, func() {
		log.Warn().Str("table", s.table).Str("schema", s.schema).Str("column", f.ColumnName).Str("type", f.DataType).
			Msg("no reader is written for this parent key type, reading it as text. Check the inserted values if the type is not a text one")
	})
	return NewScannedString()
}

func (s *sampleCommon) Init(fields []db.Field, schema, tablename, constraintName string, values [][]Getter, tableSize int64, fkCli *ForeignKeyLinks) {

	s.table = tablename
	s.schema = schema
	s.constraintName = constraintName
	s.limit = len(values)
	s.values = values
	s.fields = fields
	s.tableSize = tableSize
	s.fkCli = fkCli
}

// relationshipKey names one parent/foreign-key pair, so that a note about it
// is logged once instead of once per bulk: a sampler is rebuilt for each one.
func (s *sampleCommon) relationshipKey() string {
	return s.schema + "." + s.table + "/" + s.constraintName
}

var loggedOnce sync.Map

func logOnce(key string, f func()) {
	if _, alreadyLogged := loggedOnce.LoadOrStore(key, struct{}{}); !alreadyLogged {
		f()
	}
}

// uniformCursor is the paging state of one sequential relationship, shared by
// every bulk of the run. Only the offset is shared: the sampler itself is
// rebuilt for each bulk, so two workers cannot hand each other's rows to the
// wrong insert.
type uniformCursor struct {
	mutex   sync.Mutex
	offset  int64
	wrapped bool
}

type UniformSample struct {
	sampleCommon
	cursor *uniformCursor // paging by offset is bad, but it will work with compound pk, lack of pk, or complex pk types
}

// Sample reads the next page of the parent, wrapping back to its first row
// once the whole table has been handed out.
//
// A sequential relationship is a 1-1 one for as long as the parent has rows
// left. Past that it becomes a round robin, which is the closest thing to what
// was asked for: paging further used to select nothing and leave the insert
// with unfilled columns.
func (s *UniformSample) Sample() error {

	for filled := 0; filled < len(s.values); {
		offset, want := s.nextPage(len(s.values) - filled)

		query := fmt.Sprintf("SELECT %s FROM %s.%s WHERE %s ORDER BY 1 LIMIT %d OFFSET %d",
			db.EscapedNamesListFromFields(s.fields), db.Escape(s.schema), db.Escape(s.table),
			db.EscapedFieldsIsNotNull(s.fields), want, offset)

		if err := s.query(query, s.values[filled:filled+want]); err != nil {
			return err
		}
		filled += want
	}
	return nil
}

// nextPage reserves the next stretch of the parent's rows for this caller,
// keeping a page from straddling the end of the table: a page reaching past it
// comes back short, and the rows it did not bring back would be filled by
// repeating the ones it did.
func (s *UniformSample) nextPage(want int) (offset int64, size int) {
	s.cursor.mutex.Lock()
	defer s.cursor.mutex.Unlock()

	if s.tableSize > 0 {
		if s.cursor.offset >= s.tableSize {
			s.cursor.offset = 0
			if !s.cursor.wrapped {
				s.cursor.wrapped = true
				log.Info().Str("table", s.table).Str("schema", s.schema).Int64("parentRows", s.tableSize).
					Msgf("every row of %s has been used once by the %s relationship, starting over from its first row. A sequential relationship is 1-1 only while the parent has rows left", s.table, SequentialFlag)
			}
		}
		if int64(want) > s.tableSize-s.cursor.offset {
			want = int(s.tableSize - s.cursor.offset)
		}
	}

	offset = s.cursor.offset
	s.cursor.offset += int64(want)
	return offset, want
}

var storedUniformCursors = map[string]*uniformCursor{}
var storedUniformCursorsMutex = sync.Mutex{}

func NewUniformSample(fields []db.Field, schema, tablename, constraintName string, values [][]Getter, tableSize int64, fkCli *ForeignKeyLinks) Sampler {
	s := &UniformSample{}
	s.Init(fields, schema, tablename, constraintName, values, tableSize, fkCli)

	storedUniformCursorsMutex.Lock()
	defer storedUniformCursorsMutex.Unlock()
	cursor, ok := storedUniformCursors[tablename+constraintName]
	if !ok {
		cursor = &uniformCursor{}
		storedUniformCursors[tablename+constraintName] = cursor
	}
	s.cursor = cursor
	return s
}

type DBRandomSample struct {
	sampleCommon
	coinFlipPercent float64
}

func (s *DBRandomSample) Sample() error {

	query := fmt.Sprintf("SELECT %s FROM %s.%s %s AND %s ORDER BY 1 LIMIT %d",
		db.EscapedNamesListFromFields(s.fields), db.Escape(s.schema), db.Escape(s.table), db.BinomialWhereClause(s.coinFlipPercent), db.EscapedFieldsIsNotNull(s.fields), s.limit)

	return s.query(query, s.values)
}

func NewDBRandomSample(fields []db.Field, schema, tablename, constraintName string, values [][]Getter, tableSize int64, fkCli *ForeignKeyLinks) Sampler {
	s := &DBRandomSample{}
	s.Init(fields, schema, tablename, constraintName, values, tableSize, fkCli)
	s.coinFlipPercent = s.guardedCoinFlipPercent(fkCli.CoinFlipPercent)
	return s
}

// guardedCoinFlipPercent raises --coin-flip-percent when the parent this
// relationship samples is too small for it to bring anything back.
//
// A coin flip of p percent over a parent of N rows brings back about N*p/100
// of them, so how low the percentage can go is decided by the parent being
// read. Measured against --rows, the size of the table being filled, the guard
// never fired for a small parent feeding a large child, which is the one shape
// that needs it: 1% of a 500-row dimension table is five rows on average and
// none of them often enough to matter, and a sample bringing nothing back
// fills no foreign key.
//
// Only emptiness is guarded against. A percentage large enough to sample
// reliably is left exactly as it was asked for, even when the sample then has
// to be repeated to fill a whole bulk: favouring the hot rows of a table is
// what the flag is for.
func (s *sampleCommon) guardedCoinFlipPercent(asked float64) float64 {
	if s.tableSize <= 0 {
		return asked
	}

	minimum := math.Min(100, 100*float64(minBernoulliCandidates)/float64(s.tableSize))
	if asked >= minimum {
		return asked
	}

	logOnce("coinFlipGuard:"+s.relationshipKey(), func() {
		log.Info().Str("parent", s.table).Int64("parentRows", s.tableSize).Float64("coinFlipPercent", minimum).
			Msgf("raising --coin-flip-percent from %g to %g for %s: a %g%% coin flip over its %d rows is expected to bring back fewer than %d, and often none at all", asked, minimum, s.table, asked, s.tableSize, minBernoulliCandidates)
	})
	return minimum
}

type BoxMullerSample struct {
	sampleCommon
	stddev float64
	mean   float64
}

// box muller
// currently has a "distribution" bug I cannot figure out, there's a spike of probability around what should have been the 25 quartile
// maybe it's tied to the fact boxmuller expects [0.0,1.0] for u1 u2, but golang can only provide [0.0,1.0[
// stddev/mean does not affect it, it does not look like a float related issues but it most probably is
func (s *BoxMullerSample) Sample() error {

	rowNumbers := make([]string, s.limit)
	for i := range rowNumbers {
		// The law reaches past both ends of the table, so a row number
		// landing outside it is drawn again. Each attempt needs a new pair of
		// uniforms: testing the same one again can only give the same row
		// number back, and did so forever.
		var cosId int64 = -1
		for attempt := 0; cosId < 0 || cosId > s.tableSize; attempt++ {
			if attempt == maxNormalDraws {
				// A mean sitting far outside the table, as --normal-mean set
				// by hand leaves it, would be redrawn for a very long time.
				// The nearest row it can reach stays closer to what was asked
				// than looping does.
				cosId = min(max(int64(math.Round(s.mean)), 0), s.tableSize)
				log.Debug().Float64("mean", s.mean).Float64("stddev", s.stddev).Int64("tableSize", s.tableSize).Int64("rowNumber", cosId).Str("tablename", s.table).Msg("the normal law falls outside the table, sampling its closest row")
				break
			}
			x1, x2 := rand.Float64(), rand.Float64()
			cosId = int64(math.Round(s.mean + s.stddev*math.Sqrt(-2*math.Log(x1))*math.Cos(2*math.Pi*x2)))
		}
		rowNumbers[i] = strconv.FormatInt(cosId, 10)
	}

	escapedFields := db.EscapedNamesListFromFields(s.fields)
	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s IN (%s) AND %s LIMIT %d",
		escapedFields,
		db.FilterOnRowNumberFromClause(s.fields, s.table, s.schema),
		db.FilterOnRowNumberVarClause(),
		strings.Join(rowNumbers, ","),
		db.EscapedFieldsIsNotNull(s.fields),
		s.limit,
	)

	return s.query(query, s.values)
}

func NewBoxMullerSample(fields []db.Field, schema, tablename, constraintName string, values [][]Getter, tableSize int64, fkCli *ForeignKeyLinks) Sampler {
	s := &BoxMullerSample{}
	s.Init(fields, schema, tablename, constraintName, values, tableSize, fkCli)

	// The law draws row numbers of the parent, so its defaults are taken from
	// the parent's size. Taken from --rows, the size of the table being
	// filled, the mean of a small parent landed outside it and every draw had
	// to be redrawn.
	s.stddev = fkCli.NormalStddev
	if s.stddev == 0 {
		s.stddev = float64(tableSize) / 10
		logOnce("normalStddev:"+s.relationshipKey(), func() {
			log.Info().Str("parent", tablename).Int64("parentRows", tableSize).Msgf("setting --normal-stddev to %.2f for %s (its row count / 10) by default", s.stddev, tablename)
		})
	}
	s.mean = fkCli.NormalMean
	if s.mean == 0 {
		s.mean = float64(tableSize) / 2
		logOnce("normalMean:"+s.relationshipKey(), func() {
			log.Info().Str("parent", tablename).Int64("parentRows", tableSize).Msgf("setting --normal-mean to %.2f for %s (the middle of the table) by default", s.mean, tablename)
		})
	}
	return s
}

type ZipfSample struct {
	sampleCommon
	zipfRand *rand.Zipf
}

func (s *ZipfSample) Sample() error {

	rowNumbers := make([]string, s.limit)
	for i := range rowNumbers {
		rowNumbers[i] = strconv.Itoa(int(s.zipfRand.Uint64()))
	}
	escapedFields := db.EscapedNamesListFromFields(s.fields)
	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s IN (%s) AND %s LIMIT %d",
		escapedFields,
		db.FilterOnRowNumberFromClause(s.fields, s.table, s.schema),
		db.FilterOnRowNumberVarClause(),
		strings.Join(rowNumbers, ","),
		db.EscapedFieldsIsNotNull(s.fields),
		s.limit,
	)

	return s.query(query, s.values)
}

func NewZipfSample(fields []db.Field, schema, tablename, constraintName string, values [][]Getter, tableSize int64, fkCli *ForeignKeyLinks) Sampler {
	s := &ZipfSample{}
	s.Init(fields, schema, tablename, constraintName, values, tableSize, fkCli)
	s.zipfRand = rand.NewZipf(rand.New(rand.NewSource(time.Now().UnixNano())), fkCli.ParetoS, fkCli.ParetoV, uint64(tableSize))

	return s
}
