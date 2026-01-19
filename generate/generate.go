package generate

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"github.com/ylacancellera/random-data-load/db"
)

type Insert struct {
	table        *db.Table
	writer       io.Writer
	NotifyChan   chan int64
	fklinks      ForeignKeyLinks
	workersCount int
	insertMutex  sync.Mutex
	maxTextSize  int64
	uuidVersion  int
	maxRetries   int
}

type ForeignKeyLinks struct {
	DefaultRelationship string            `name:"default-relationship" help:"Will define the default foreign-key relationship to apply. Possible values: ${BinomialFlag},${SequentialFlag}. The default relation can be overriden with other parameters --${BinomialFlag} or --${SequentialFlag}" enum:"${BinomialFlag},${SequentialFlag}" default:"${BinomialFlag}"`
	Binomial            map[string]string ` help:"Defines a 1-N foreign key relationships using repeated coin flips. Postgres' tablesamples Bernouilli or mysql RAND() < 0.1 (can be tuned with --coin-flip-percent). Format should be \"parent_table=child_table\" E.g: --${BinomialFlag}=\"customers=orders;orders=items\""`
	Sequential          map[string]string `name:"sequential" help:"Defines a sequential foreign key links relationships, using SELECT ... LIMIT x OFFET y. Format should be \"parent_table=child_table\" E.g: --${SequentialFlag}=\"citizens=ssns\""`
	CoinFlipPercent     float64           `name:"coin-flip-percent" help:"When used with ${BinomialFlag}, it will set the likeliness of each rows to be sampled or not. 10 would mean each rows have only 10%% chance to be selected when sampling a parent table. Using large values will favor hot rows: the coin flips are done with a table full scan, with a limit set at --bulk-size, so with a large percent chance most of the time the first rows will be selected. No effects when used with --${SequentialFlag}. Lower value (e.g 0.001) will also slow down the sampling speed" default:"1"`
}

const (
	SequentialFlag = "sequential"
	BinomialFlag   = "binomial"
)

var fkLinkToSamplerCreator = map[string]SamplerBuilder{
	SequentialFlag: NewUniformSample,
	BinomialFlag:   NewDBRandomSample,
}

func (r ForeignKeyLinks) relationship(parent, child string) SamplerBuilder {
	if r.Sequential[parent] == child {
		return fkLinkToSamplerCreator[SequentialFlag]
	}
	if r.Binomial[parent] == child {
		return fkLinkToSamplerCreator[BinomialFlag]
	}
	return fkLinkToSamplerCreator[r.DefaultRelationship]
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
func New(table *db.Table, fklinks ForeignKeyLinks, workersCount int, maxTextSize int64, uuidVersion int) *Insert {
	in := &Insert{
		table:        table,
		writer:       os.Stdout,
		fklinks:      fklinks,
		workersCount: workersCount,
		maxTextSize:  maxTextSize,
		uuidVersion:  uuidVersion,
		maxRetries:   5,
	}
	in.NotifyChan = make(chan int64)
	return in
}

// SetWriter lets you specify a custom writer. The default is Stdout.
func (in *Insert) SetWriter(w io.Writer) {
	in.writer = w
}

// Run starts the insert process.
func (in *Insert) Run(count, bulksize int64) error {
	return in.run(count, bulksize, false)
}

// DryRun starts writing the generated queries to the specified writer.
func (in *Insert) DryRun(count, bulksize int64) error {
	return in.run(count, bulksize, true)
}

func (in *Insert) run(count int64, bulksize int64, dryRun bool) error {
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
	numJobs := completeInserts + 1 // + remainder

	bulksizeJobs := make(chan int64, numJobs)
	errChan := make(chan error, numJobs)
	defer close(bulksizeJobs)
	defer close(errChan)

	for w := 1; w <= in.workersCount; w++ {
		go in.worker(errChan, bulksizeJobs, dryRun)
	}

	for i := int64(0); i < completeInserts; i++ {
		bulksizeJobs <- bulksize
	}

	bulksizeJobs <- remainder

	for j := 1; j <= int(numJobs); j++ {
		err := <-errChan
		if err != nil {
			return err
		}
	}

	return nil
}

func (in *Insert) worker(errChan chan<- error, bulksizeJobs <-chan int64, dryRun bool) {
	for bulksize := range bulksizeJobs {
		tries := 0
		for {
			n, err := in.insert(bulksize, dryRun)
			if err == nil {
				in.notify(n)
				errChan <- nil
				break
			}
			if !db.ErrShouldRetryTx(err) {
				errChan <- errors.Wrap(err, "failed to bulk insert")
				return
			}
			if tries == in.maxRetries {
				errChan <- errors.Wrapf(err, "failed after %d retries", in.maxRetries)
				return
			}
			tries += 1
			log.Debug().Msgf("Looping the transaction due to '%v'", err)
		}
	}
}

func (in *Insert) notify(n int64) {
	if in.NotifyChan != nil {
		select {
		case in.NotifyChan <- n:
		default:
		}
	}
}

// generate field and sample fields in parallel, since both operations are slow
func (in *Insert) genQuery(count int64) *string {

	if count < 1 {
		return nil
	}

	fieldsAsDefault := in.table.FieldsToInsertAsDefault()
	fieldsToGen := in.table.FieldsToGenerate()
	constraintsToSample := in.table.ConstraintsToSample()
	fieldsToSample := constraintsToSample.Fields()
	var insertQuery strings.Builder
	_, err := insertQuery.WriteString(fmt.Sprintf(db.InsertTemplate(), //nolint
		db.Escape(in.table.Schema),
		db.Escape(in.table.Name),
		db.EscapedNamesListFromFields(slices.Concat(fieldsAsDefault, fieldsToGen, fieldsToSample)),
	))
	if err != nil {
		log.Error().Err(err).Msg("failed to build string")
	}
	log.Debug().Str("fieldsAsDefault", db.EscapedNamesListFromFields(fieldsAsDefault)).
		Str("fieldsToGen", db.EscapedNamesListFromFields(fieldsToGen)).
		Str("fieldsToSample", db.EscapedNamesListFromFields(fieldsToSample)).
		Str("table", in.table.Name).Str("schema", in.table.Schema).Msg("genQuery init")

	// TODO obj pool ?
	// full init of the 2 layer slice
	values := make([]InsertValues, count)
	for i := range values {
		values[i] = make(InsertValues, len(fieldsAsDefault)+len(fieldsToGen)+len(fieldsToSample))
	}

	var wg sync.WaitGroup
	// fields order; DEFAULTs, then generated, then sampled
	idxFieldsAsDefault := len(fieldsAsDefault)
	idxFieldsToGen := idxFieldsAsDefault + len(fieldsToGen)

	if len(fieldsAsDefault) != 0 {
		wg.Add(1)
		go func() {
			for i := int64(0); i < count; i++ {
				for col := int64(0); col < int64(idxFieldsAsDefault); col++ {
					values[i][col] = NewDefaultKeyword()
				}
			}
			wg.Done()
		}()
	}

	if len(fieldsToGen) != 0 {
		wg.Add(1)
		go func() {
			for i := int64(0); i < count; i++ {
				in.generateFieldsRow(fieldsToGen, values[i][idxFieldsAsDefault:idxFieldsToGen])
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
			sampledValues := make([][]Getter, count)
			for i := range sampledValues {
				sampledValues[i] = values[i][idxFieldsToGen:]
			}
			err := in.sampleConstraints(constraintsToSample, sampledValues)
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

	in.insertMutex.Lock()
	defer in.insertMutex.Unlock()
	res, err := db.DB.Exec(*insertQuery)
	if err != nil {
		return 0, err
	}
	ra, _ := res.RowsAffected()
	return ra, err
}

func (in *Insert) generateFieldsRow(fields []db.Field, insertValues []Getter) {
	for colIndex := range insertValues {
		field := fields[colIndex]
		var value Getter
		switch field.DataType {
		case "bool", "boolean":
			value = NewRandomBool(field.ColumnName, field.IsNullable)
		case "tinyint", "bit":
			value = NewRandomIntRange(field.ColumnName, 0, 1, field.IsNullable)
		case "smallint", "mediumint", "int", "integer", "bigint":
			maxValue := maxValues["bigint"]
			if m, ok := maxValues[field.DataType]; ok {
				maxValue = m
			}
			value = NewRandomInt(field.ColumnName, maxValue, field.IsNullable)
		case "float", "decimal", "double", "numeric":
			value = NewRandomDecimal(field.ColumnName, field.NumericPrecision.Int64, field.NumericScale.Int64, field.IsNullable)
		case "date":
			value = NewRandomDate(field.ColumnName, field.IsNullable)
		case "datetime", "timestamp":
			value = NewRandomDateTime(field.ColumnName, field.IsNullable)
		case "uuid":
			value = NewRandomUUID(field.ColumnName, in.uuidVersion, field.IsNullable)
		case "char", "varchar", "tinyblob", "tinytext", "blob", "text", "mediumtext", "mediumblob", "longblob", "longtext":
			maxSize := in.maxTextSize
			if maxSize > field.CharacterMaximumLength.Int64 {
				maxSize = field.CharacterMaximumLength.Int64
			}
			value = NewRandomString(field.ColumnName, maxSize, field.IsNullable)
		case "time":
			value = NewRandomTime(field.IsNullable)
		case "year":
			value = NewRandomIntRange(field.ColumnName, int64(time.Now().Year()-1),
				int64(time.Now().Year()), field.IsNullable)
		case "enum", "set":
			value = NewRandomEnum(field.SetEnumVals, field.IsNullable)
		case "binary", "varbinary":
			value = NewRandomBinary(field.ColumnName, field.CharacterMaximumLength.Int64, field.IsNullable)
		default:
			log.Error().Str("type", field.DataType).Str("field", field.ColumnName).Msg("unsupported datatypes when generating fields")
		}
		insertValues[colIndex] = value
	}
}

func (in *Insert) sampleConstraints(constraints db.Constraints, values [][]Getter) error {

	colIdx := 0

	var err error
	for _, constraint := range constraints {

		// subslice stores only a few columns grouped together with the FK columns
		subSlice := make([][]Getter, len(values))
		for i := range subSlice {
			subSlice[i] = values[i][colIdx : colIdx+len(constraint.ReferencedFields)]
		}

		samplerInit := in.fklinks.relationship(constraint.ReferencedTableName, in.table.Name)
		sampler := samplerInit(constraint.ReferencedFields, constraint.ReferencedTableSchema, constraint.ReferencedTableName, constraint.ConstraintName, subSlice, in.fklinks.CoinFlipPercent)
		err = sampler.Sample()
		if err != nil {
			return errors.Wrap(err, "sampleFieldsTable")
		}
		colIdx += len(constraint.ReferencedFields)

	}
	return nil
}
