package getters

import (
	"database/sql"
	"fmt"

	"github.com/pkg/errors"
	"github.com/ylacancellera/random-data-load/db"
)

type Sampler interface {
	Sample() error
}

type sampleCommon struct {
	schema string
	table  string
	fields []db.Field
	db     *sql.DB
}

func (s *sampleCommon) query(query string, values [][]Getter) error {

	rows, err := s.db.Query(query)
	if err != nil {
		return fmt.Errorf("cannot get samples: %s, %s", query, err)
	}
	defer rows.Close()

	var rowIndex int
	scannedValuesInterface := make([]interface{}, len(s.fields))
	scannedGetter := make([]ScannerGetter, len(s.fields))
	for fieldIndex, field := range s.fields {
		getter := s.getterFromField(field)
		scannedGetter[fieldIndex] = getter
		scannedValuesInterface[fieldIndex] = &getter
	}

	for rows.Next() {
		err = rows.Scan(scannedValuesInterface...)
		if err != nil {
			return errors.Wrap(err, "failed to scan samples")
		}
		for fieldIndex := range s.fields {
			//getter := scannedValuesInterface[fieldIndex].(getterType)
			values[rowIndex][fieldIndex] = scannedGetter[fieldIndex]
		}

		rowIndex = rowIndex + 1
	}
	if rowIndex == 0 {
		return fmt.Errorf("cannot get samples: %s", errors.Errorf("table %s was empty", "TODO"))
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
	lastOffset int // paging by offset is bad, but it prevents dealing with compound pk, lack of pk, or complex pk types
}

func (s *UniformSample) Sample() error {

	//var count int64
	//var query string

	//count, ok := storedSampleCount[in.table.Schema+"#"+in.table.Name]
	//if !ok {
	//	queryCount := fmt.Sprintf("SELECT COUNT(*) FROM %s.%s", db.Escape(in.table.Schema), db.Escape(in.table.Name))
	//	if err := in.db.QueryRow(queryCount).Scan(&count); err != nil {
	//		return fmt.Errorf("cannot get count for table %q: %s", in.table.Name, err)
	//	}
	//	storedSampleCount[in.table.Schema+"#"+in.table.Name] = count
	//}

	query := fmt.Sprintf("SELECT %s FROM %s.%s LIMIT %d OFFSET %d",
		db.EscapedNamesListFromFields(s.fields), db.Escape(s.schema), db.Escape(s.table), s.limit, s.lastOffset)

	s.lastOffset += s.limit
	return s.query(query, s.values)
}

var storedUniformSamples = map[string]*UniformSample{}

func NewUniformSample(db *sql.DB, fields []db.Field, name, schema string, values [][]Getter) *UniformSample {
	if r, ok := storedUniformSamples[name]; ok {
		r.values = values
		return r
	}
	r := &UniformSample{}
	r.table = name
	r.schema = schema
	r.limit = len(values)
	r.values = values
	r.db = db
	r.fields = fields
	storedUniformSamples[name] = r
	return r
}
