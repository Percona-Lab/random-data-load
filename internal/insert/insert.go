package insert

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"github.com/ylacancellera/random-data-load/db"
	"github.com/ylacancellera/random-data-load/internal/getters"
)

type Insert struct {
	db         *sql.DB
	table      *db.Table
	writer     io.Writer
	notifyChan chan int64
}

var (
	maxValues = map[string]int64{
		"tinyint":   0xF,
		"smallint":  0xFF,
		"mediumint": 0x7FFFF,
		"int":       0x7FFFFFFF,
		"integer":   0x7FFFFFFF,
		"float":     0x7FFFFFFF,
		"decimal":   0x7FFFFFFF,
		"double":    0x7FFFFFFF,
		"bigint":    0x7FFFFFFFFFFFFFFF,
	}
)

// New returns a new Insert instance.
func New(db *sql.DB, table *db.Table) *Insert {
	return &Insert{
		db:     db,
		table:  table,
		writer: os.Stdout,
	}
}

// SetWriter lets you specify a custom writer. The default is Stdout.
func (in *Insert) SetWriter(w io.Writer) {
	in.writer = w
}

func (in *Insert) NotifyChan() chan int64 {
	if in.notifyChan != nil {
		close(in.notifyChan)
	}

	in.notifyChan = make(chan int64)
	return in.notifyChan
}

// Run starts the insert process.
func (in *Insert) Run(count, bulksize int64) (int64, error) {
	return in.run(count, bulksize, false)
}

// DryRun starts writing the generated queries to the specified writer.
func (in *Insert) DryRun(count, bulksize int64) (int64, error) {
	return in.run(count, bulksize, true)
}

func (in *Insert) run(count int64, bulksize int64, dryRun bool) (int64, error) {
	if in.notifyChan != nil {
		defer close(in.notifyChan)
	}

	// Example: want 11 rows with bulksize 4:
	// count = int(11 / 4) = 2 -> 2 bulk inserts having 4 rows each = 8 rows
	// We need to run this insert twice:
	// INSERT INTO table (f1, f2) VALUES (?, ?), (?, ?), (?, ?), (?, ?)
	//                                      1       2       3       4

	// remainder = rows - count = 11 - 8 = 3
	// And then, we need to run this insert once to complete 11 rows
	// INSERT INTO table (f1, f2) VALUES (?, ?), (?, ?), (?, ?)
	//                                     1        2       3
	completeInserts := count / bulksize
	remainder := count - completeInserts*bulksize

	var n, okCount int64
	var err error

	for i := int64(0); i < completeInserts; i++ {
		n, err = in.insert(bulksize, dryRun)
		okCount += n
		if err != nil {
			return okCount, err
		}
		in.notify(n)
	}

	n, err = in.insert(remainder, dryRun)
	okCount += n
	in.notify(n)

	return okCount, err
}

func (in *Insert) notify(n int64) {
	if in.notifyChan != nil {
		select {
		case in.notifyChan <- n:
		default:
		}
	}
}

// generate field and sample fields in parallel, since both operations are slow
func (in *Insert) genQuery(count int64) *string {

	if count < 1 {
		return nil
	}

	fieldsToGen := in.table.FieldsToGenerate()
	fieldsToSample := in.table.FieldsToSample()
	var insertQuery strings.Builder
	_, err := insertQuery.WriteString(fmt.Sprintf(db.InsertTemplate(), //nolint
		db.Escape(in.table.Schema),
		db.Escape(in.table.Name),
		db.EscapedNamesListFromFields(append(fieldsToGen, fieldsToSample...)),
	))
	if err != nil {
		log.Error().Err(err).Msg("failed to build string")
	}
	log.Debug().Str("fieldsToGen", db.EscapedNamesListFromFields(fieldsToGen)).Str("fieldsToSample", db.EscapedNamesListFromFields(fieldsToSample)).Str("table", in.table.Name).Str("schema", in.table.Schema).Msg("genQuery init")

	// TODO obj pool ?
	// full init of the 2 layer slice
	values := make([]getters.InsertValues, count)
	for i := range values {
		values[i] = make(getters.InsertValues, len(fieldsToGen)+len(fieldsToSample))
	}

	var wg sync.WaitGroup

	if len(fieldsToGen) != 0 {
		wg.Add(1)
		go func() {
			for i := int64(0); i < count; i++ {
				generateFieldsRow(fieldsToGen, values[i][:len(fieldsToGen)])
			}
			wg.Done()
		}()
	}

	if len(fieldsToSample) != 0 {
		wg.Add(1)
		go func() {

			// prep a "subslice" of the 2 layer slice
			// that way each rows (1st layer) only gets the sublice of the fields to sample
			// it ensures each goroutines work on the main "values" array without overlaps
			sampledValues := make([][]getters.Getter, count)
			for i := range sampledValues {
				sampledValues[i] = values[i][len(fieldsToGen):]
			}
			err := in.sampleFieldsTable(fieldsToSample, sampledValues)
			if err != nil {
				log.Error().Err(err).Msg("error when sampling field")
			}
			wg.Done()
		}()
	}

	wg.Wait()
	for row := range values {
		if values[row] == nil {
			continue
		}
		insertQuery.WriteString(values[row].String())
		if row != len(values)-1 {
			insertQuery.WriteString(",")
		}
	}
	s := insertQuery.String()
	return &s
}

func (in *Insert) insert(count int64, dryRun bool) (int64, error) {

	if count < 1 {
		return 0, nil
	}

	insertQuery := in.genQuery(count)

	if dryRun {
		if _, err := in.writer.Write([]byte(*insertQuery + "\n")); err != nil {
			return 0, err
		}
		return count, nil
	}

	res, err := in.db.Exec(*insertQuery)
	if err != nil {
		log.Error().Str("query", *insertQuery).Err(err).Msg("failed to insert")
		return 0, err
	}
	ra, _ := res.RowsAffected()
	return ra, err
}

func generateFieldsRow(fields []db.Field, insertValues []getters.Getter) {
	for colIndex := range insertValues {
		field := fields[colIndex]
		var value getters.Getter
		switch field.DataType {
		case "tinyint", "bit", "bool", "boolean":
			value = getters.NewRandomIntRange(field.ColumnName, 0, 1, field.IsNullable)
		case "smallint", "mediumint", "int", "integer", "bigint":
			maxValue := maxValues["bigint"]
			if m, ok := maxValues[field.DataType]; ok {
				maxValue = m
			}
			value = getters.NewRandomInt(field.ColumnName, maxValue, field.IsNullable)
		case "float", "decimal", "double", "numeric":
			value = getters.NewRandomDecimal(field.ColumnName, field.NumericPrecision.Int64, field.NumericScale.Int64, field.IsNullable)
		case "char", "varchar":
			value = getters.NewRandomString(field.ColumnName, field.CharacterMaximumLength.Int64, field.IsNullable)
		case "date":
			value = getters.NewRandomDate(field.ColumnName, field.IsNullable)
		case "datetime", "timestamp":
			value = getters.NewRandomDateTime(field.ColumnName, field.IsNullable)
		case "tinyblob", "tinytext", "blob", "text", "mediumtext", "mediumblob", "longblob", "longtext":
			value = getters.NewRandomString(field.ColumnName, field.CharacterMaximumLength.Int64, field.IsNullable)
		case "time":
			value = getters.NewRandomTime(field.IsNullable)
		case "year":
			value = getters.NewRandomIntRange(field.ColumnName, int64(time.Now().Year()-1),
				int64(time.Now().Year()), field.IsNullable)
		case "enum", "set":
			value = getters.NewRandomEnum(field.SetEnumVals, field.IsNullable)
		case "binary", "varbinary":
			value = getters.NewRandomBinary(field.ColumnName, field.CharacterMaximumLength.Int64, field.IsNullable)
		default:
			log.Error().Str("type", field.DataType).Str("field", field.ColumnName).Msg("unsupported datatypes when generating fields")
		}
		insertValues[colIndex] = value
	}
}

func (in *Insert) sampleFieldsTable(fields []db.Field, values [][]getters.Getter) error {

	colIdx := 0

	var err error
	for _, constraint := range in.table.Constraints {

		// subslice stores only a few columns grouped together with the FK columns
		subSlice := make([][]getters.Getter, len(values))
		for i := range subSlice {
			subSlice[i] = values[i][colIdx : colIdx+len(constraint.ReferencedFields)]
		}

		sampler := getters.NewRandomSample(in.db, constraint.ReferencedFields, constraint.ReferencedTableSchema, constraint.ReferencedTableName, subSlice)
		err = sampler.Sample()
		if err != nil {
			return errors.Wrap(err, "sampleFieldsTable")
		}
		colIdx += len(constraint.ReferencedFields)

	}
	return nil
}
