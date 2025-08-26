package generate

import (
	"fmt"
	"sync"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/ylacancellera/random-data-load/db"
)

type Sampler interface {
	Sample() error
}

type SamplerBuilder func([]db.Field, string, string, string, [][]Getter) Sampler

type sampleCommon struct {
	schema string
	table  string
	fields []db.Field
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
			values[rowIdx][fieldIdx] = scannedGetter[fieldIdx]
		}

		rowIdx = rowIdx + 1
	}

	if rowIdx == 0 {
		return fmt.Errorf("cannot get samples: %s", errors.Errorf("table %s was empty", s.table))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("cannot get samples: %s", err)
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

type UniformSample struct {
	sampleCommon
	values     [][]Getter
	limit      int
	lastOffset int // paging by offset is bad, but it will work with compound pk, lack of pk, or complex pk types
	mutex      sync.Mutex
}

func (s *UniformSample) Sample() error {

	// choosing a chunk + updating lastOffset is the only part that require exclusive access
	s.mutex.Lock()
	query := fmt.Sprintf("SELECT %s FROM %s.%s LIMIT %d OFFSET %d",
		db.EscapedNamesListFromFields(s.fields), db.Escape(s.schema), db.Escape(s.table), s.limit, s.lastOffset)

	s.lastOffset += s.limit
	s.mutex.Unlock()

	return s.query(query, s.values)
}

var storedUniformSamples = map[string]*UniformSample{}

func NewUniformSample(fields []db.Field, schema, tablename, constraintName string, values [][]Getter) Sampler {
	if s, ok := storedUniformSamples[tablename+constraintName]; ok {
		s.values = values
		return s
	}
	s := &UniformSample{}
	s.table = tablename
	s.schema = schema
	s.limit = len(values)
	s.values = values
	s.fields = fields
	storedUniformSamples[tablename+constraintName] = s
	return s
}

type DBRandomSample struct {
	sampleCommon
	values [][]Getter
	limit  int
}

func (s *DBRandomSample) Sample() error {

	query := fmt.Sprintf("SELECT %s FROM %s.%s %s LIMIT %d",
		db.EscapedNamesListFromFields(s.fields), db.Escape(s.schema), db.Escape(s.table), db.DBRandomWhereClause(), s.limit)

	return s.query(query, s.values)
}

func NewDBRandomSample(fields []db.Field, schema, name, _ string, values [][]Getter) Sampler {
	s := &DBRandomSample{}
	s.table = name
	s.schema = schema
	s.limit = len(values)
	s.values = values
	s.fields = fields
	return s
}
