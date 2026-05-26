package generate

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/ylacancellera/random-data-load/db"
)

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
			scannedGetter[fieldIdx] = getter
			scannedValuesInterface[fieldIdx] = getter
		}
		err = rows.Scan(scannedValuesInterface...)
		if err != nil {
			return errors.Wrapf(err, "failed to scan samples with query %s", query)
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
		return fmt.Errorf("cannot get samples: %s", errors.Errorf("table %s was empty", s.table))
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

func (s *sampleCommon) getterFromField(f db.Field) ScannerGetter {

	switch f.DataType {
	case "tinyint", "smallint", "mediumint", "int", "integer", "bigint", "year":
		return NewScannedInt()
	case "char", "varchar", "blob", "text", "mediumtext",
		"mediumblob", "longblob", "longtext":
		return NewScannedString()
	case "binary", "varbinary":
		return NewScannedBinary()
	case "float", "decimal", "double":
		return NewScannedDecimal()
	case "date", "time", "datetime", "timestamp":
		return NewScannedTime()
	}
	return nil
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

type UniformSample struct {
	sampleCommon
	lastOffset int // paging by offset is bad, but it will work with compound pk, lack of pk, or complex pk types
	mutex      sync.Mutex
}

func (s *UniformSample) Sample() error {

	// choosing a chunk + updating lastOffset is the only part that require exclusive access
	s.mutex.Lock()
	query := fmt.Sprintf("SELECT %s FROM %s.%s WHERE %s ORDER BY 1 LIMIT %d OFFSET %d",
		db.EscapedNamesListFromFields(s.fields), db.Escape(s.schema), db.Escape(s.table), db.EscapedFieldsIsNotNull(s.fields), s.limit, s.lastOffset)

	s.lastOffset += s.limit
	s.mutex.Unlock()

	return s.query(query, s.values)
}

var storedUniformSamples = map[string]*UniformSample{}
var storedUniformSamplesMutex = sync.Mutex{}

func NewUniformSample(fields []db.Field, schema, tablename, constraintName string, values [][]Getter, tableSize int64, fkCli *ForeignKeyLinks) Sampler {
	storedUniformSamplesMutex.Lock()
	defer storedUniformSamplesMutex.Unlock()
	if s, ok := storedUniformSamples[tablename+constraintName]; ok {
		s.values = values
		return s
	}
	s := &UniformSample{}
	s.Init(fields, schema, tablename, constraintName, values, tableSize, fkCli)
	storedUniformSamples[tablename+constraintName] = s
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
	s.coinFlipPercent = fkCli.CoinFlipPercent
	return s
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
		var cosId int64 = -1
		x1, x2 := rand.Float64(), rand.Float64()
		for cosId < 0 || cosId > s.tableSize {
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

	s.stddev = fkCli.NormalStddev
	s.mean = fkCli.NormalMean
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
