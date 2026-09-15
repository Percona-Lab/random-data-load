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

	"github.com/Percona-Lab/random-data-load/db"
	"github.com/Percona-Lab/random-data-load/frequency"
)

type Insert struct {
	table            *db.Table
	writer           io.Writer
	NotifyChan       chan int64
	fklinks          ForeignKeyLinks
	workersCount     int
	insertMutex      sync.Mutex
	maxTextSize      int64
	uuidVersion      int
	maxRetries       int
	frequencies      frequency.ColumnFrequency
	minGeneratedTime *time.Time
	maxGeneratedTime *time.Time
	widthTarget      *rowWidthTarget
	rowsWanted       int64
}

type ForeignKeyLinks struct {
	DefaultRelationship string            `name:"default-relationship" help:"Will define the default foreign-key relationship to apply. Possible values: ${BinomialFlag},${SequentialFlag}. The default relation can be overriden with other parameters --${BinomialFlag} or --${SequentialFlag}" enum:"${BinomialFlag},${SequentialFlag},${NormalFlag},${ParetoFlag}" default:"${BinomialFlag}"`
	Binomial            map[string]string ` help:"Defines a 1-N foreign key relationships using repeated coin flips. Postgres' tablesamples Bernouilli or mysql RAND() < 0.1 (can be tuned with --coin-flip-percent). Format should be \"parent_table=child_table\" E.g: --${BinomialFlag}=\"customers=orders;orders=items\""`
	Sequential          map[string]string `name:"sequential" help:"Defines a sequential foreign key links relationships, using SELECT ... LIMIT x OFFET y. Format should be \"parent_table=child_table\" E.g: --${SequentialFlag}=\"citizens=ssns\""`
	CoinFlipPercent     PerTableFloat     `name:"coin-flip-percent" help:"When used with ${BinomialFlag}, it will set the likeliness of each rows to be sampled or not. 10 would mean each rows have only 10% chance to be selected when sampling a parent table. Using large values will favor hot rows: the coin flips are done with a table full scan, with a limit set at --bulk-size, so with a large percent chance most of the time the first rows will be selected. No effects when used with --${SequentialFlag}. Lower value (e.g 0.001) will also slow down the sampling speed. The right value depends on the parent being sampled, so it can be given per parent table: --coin-flip-percent=\"1;orders=3;products=5\"" default:"1"`
	Normal              map[string]string `help:"Defines a 1-N foreign key relationships using box-muller transformation to provide normal distribution. Slow method needing full table scans for each samples."`
	NormalStddev        PerTableFloat     `help:"Standard deviation to the normal law. Will default to 1/10 of the row count of the parent table being sampled. Can be given per parent table: --normal-stddev=\"orders=5000;products=250\""`
	NormalMean          PerTableFloat     `help:"Mean of the normal law. Will default to the middle of the parent table being sampled. Can be given per parent table: --normal-mean=\"orders=50000;products=1500\""`
	Pareto              map[string]string `help:"Defines a 1-N foreign key relationships using zipf (pareto) distribution. Slow method needing full table scans for each samples"`
	ParetoS             PerTableFloat     `help:"Zipf slope parameter. Must be above 1. Higher value will mean faster decay, so first rows will be hotter. Can be given per parent table: --pareto-s=\"1.1;orders=1.4\"" default:"1.1"`
	ParetoV             PerTableFloat     `help:"Must be >=1. Directly map to V, https://pkg.go.dev/math/rand#Zipf. Can be given per parent table." default:"1.0"`
}

const (
	SequentialFlag = "sequential"
	BinomialFlag   = "binomial"
	NormalFlag     = "normal"
	ParetoFlag     = "pareto"
)

var fkLinkToSamplerCreator = map[string]SamplerBuilder{
	SequentialFlag: NewUniformSample,
	BinomialFlag:   NewDBRandomSample,
	NormalFlag:     NewBoxMullerSample,
	ParetoFlag:     NewZipfSample,
}

func (r ForeignKeyLinks) relationship(parent, child string) SamplerBuilder {
	if r.Sequential[parent] == child {
		return fkLinkToSamplerCreator[SequentialFlag]
	}
	if r.Binomial[parent] == child {
		return fkLinkToSamplerCreator[BinomialFlag]
	}
	if r.Normal[parent] == child {
		return fkLinkToSamplerCreator[NormalFlag]
	}
	if r.Pareto[parent] == child {
		return fkLinkToSamplerCreator[ParetoFlag]
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
func New(table *db.Table, fklinks ForeignKeyLinks, workersCount int, maxTextSize int64, uuidVersion int, freqs frequency.ColumnFrequency, minTime, maxTime *time.Time) *Insert {
	in := &Insert{
		table:            table,
		writer:           os.Stdout,
		fklinks:          fklinks,
		workersCount:     workersCount,
		maxTextSize:      maxTextSize,
		uuidVersion:      uuidVersion,
		maxRetries:       5,
		frequencies:      freqs,
		minGeneratedTime: minTime,
		maxGeneratedTime: maxTime,
	}

	in.NotifyChan = make(chan int64)
	return in
}

// SetTargetBytesPerRow aims the rows this table produces at a width.
//
// Row width decides how many rows fit in a page, page count decides scan
// costs, and scan costs decide the plan, so it is usually the figure a
// reproduction has to hit. The target is met by writing longer or shorter
// values into the columns that hold free text; what each one has to hold is
// worked out by a calibration pass before the run starts.
func (in *Insert) SetTargetBytesPerRow(bytes int64) {
	if bytes <= 0 {
		return
	}
	in.widthTarget = &rowWidthTarget{bytesPerRow: bytes}
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
	in.rowsWanted = count

	// Before anything is written: a row width target has to know how wide a
	// row comes out on its own before it can say what to add to it.
	if err := in.calibrate(); err != nil {
		return errors.Wrapf(err, "measuring the row width of %s.%s", in.table.Schema, in.table.Name)
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
	numJobs := completeInserts + 1 // + remainder

	bulksizeJobs := make(chan int64, numJobs)
	errChan := make(chan error, numJobs)
	defer close(bulksizeJobs)
	// errChan is deliberately left open. A worker that gave up reports its
	// error and returns, and the first error read here ends the run, so
	// closing it would risk a send on a closed channel, which is a panic.

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
				errChan <- errors.Wrap(err, "got error during bulk insert")
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
func (in *Insert) genQuery(count int64) (string, error) {

	if count < 1 {
		return "", nil
	}

	fields, values, err := in.buildValues(count)
	if err != nil {
		return "", err
	}

	var insertQuery strings.Builder
	_, err = insertQuery.WriteString(fmt.Sprintf(db.InsertTemplate(), //nolint
		db.Escape(in.table.Schema),
		db.Escape(in.table.Name),
		db.EscapedNamesListFromFields(fields),
	))
	if err != nil {
		log.Error().Err(err).Msg("failed to build string")
	}

	for row := range values {
		if values[row] == nil {
			continue
		}
		renderedRow, err := values[row].Render()
		if err != nil {
			return "", errors.Wrapf(err, "cannot write a row of %s.%s", in.table.Schema, in.table.Name)
		}
		insertQuery.WriteString(renderedRow)
		if row != len(values)-1 {
			insertQuery.WriteString(",")
		}
	}
	return insertQuery.String(), nil
}

// buildValues fills count rows and hands back the columns they are for, in the
// order the INSERT will list them.
//
// Split out of genQuery so that a row can be produced and looked at without
// being written anywhere: measuring how wide a row comes out needs the values
// themselves, generated and sampled exactly as a real bulk would be, and
// nothing else about the statement.
func (in *Insert) buildValues(count int64) ([]db.Field, []InsertValues, error) {
	if count < 1 {
		return nil, nil, nil
	}

	fieldsAsDefault := in.table.FieldsToInsertAsDefault()
	fieldsToGen := in.table.FieldsToGenerate()
	constraintsToSample := in.table.ConstraintsToSample()
	fieldsToSample := constraintsToSample.Fields()
	fields := slices.Concat(fieldsAsDefault, fieldsToGen, fieldsToSample)
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

	// A failed sampling leaves the columns of a foreign key unfilled, so the
	// insert cannot be written at all. It used to be logged and forgotten,
	// which left the run reporting success after inserting nothing.
	var sampleErr error
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
			sampleErr = in.sampleConstraints(constraintsToSample, sampledValues)
			wg.Done()
		}()
	}

	wg.Wait()
	if sampleErr != nil {
		return nil, nil, errors.Wrapf(sampleErr, "cannot sample the foreign keys of %s.%s", in.table.Schema, in.table.Name)
	}
	return fields, values, nil
}

func (in *Insert) insert(count int64, dryRun bool) (int64, error) {

	if count < 1 {
		return 0, nil
	}

	insertQuery, err := in.genQuery(count)
	if err != nil {
		return 0, err
	}

	if dryRun {
		if _, err := in.writer.Write([]byte(insertQuery + "\n")); err != nil {
			return 0, err
		}
		return count, nil
	}

	in.insertMutex.Lock()
	defer in.insertMutex.Unlock()
	res, err := db.DB.Exec(insertQuery)
	if err != nil {
		return 0, err
	}
	ra, _ := res.RowsAffected()
	return ra, err
}

func (in *Insert) generateFieldsRow(fields []db.Field, insertValues []Getter) {
	for colIndex := range insertValues {
		field := fields[colIndex]
		gw := NewGetterWrapper(field.ColumnName, field.IsNullable, in.frequencies)
		if gw.Elem != nil {
			goto SKIP
		}
		switch field.DataType {
		case "bool", "boolean":
			gw.Assign(NewRandomBool())
		case "tinyint", "bit":
			gw.Assign(NewRandomIntRange(0, 1))
		case "smallint", "mediumint", "int", "integer", "bigint":
			maxValue := maxValues["bigint"]
			if m, ok := maxValues[field.DataType]; ok {
				maxValue = m
			}
			gw.Assign(NewRandomInt(maxValue))
		case "float", "decimal", "double", "numeric":
			gw.Assign(NewRandomDecimal(field.NumericPrecision, field.NumericScale))
		case "date", "datetime", "timestamp":
			gw.Assign(NewRandomDate(in.minGeneratedTime, in.maxGeneratedTime))
		case "time":
			gw.Assign(NewRandomTime())
		case "uuid":
			gw.Assign(NewRandomUUID(in.uuidVersion))
		case "json", "jsonb":
			gw.Assign(NewRandomJSON(field.ColumnName))
		case "char", "varchar", "tinyblob", "tinytext", "blob", "text", "mediumtext", "mediumblob", "longblob", "longtext":
			maxSize := in.maxTextSize
			if maxSize > field.CharacterMaximumLength.Int64 {
				maxSize = field.CharacterMaximumLength.Int64
			}
			if length, filled := in.widthTarget.lengthFor(field.ColumnName); filled {
				gw.Assign(NewFilledString(field.ColumnName, length, maxSize))
				break
			}
			gw.Assign(NewRandomString(field.ColumnName, maxSize))
		case "year":
			// TODO: meh.
			gw.Assign(NewRandomIntRange(int64(in.minGeneratedTime.Year()), int64(in.maxGeneratedTime.Year())))
		case "enum", "set":
			gw.Assign(NewRandomEnum(field.SetEnumVals))
		case "binary", "varbinary":
			gw.Assign(NewRandomBinary(field.CharacterMaximumLength.Int64))
		default:
			log.Error().Str("type", field.DataType).Str("field", field.ColumnName).Msg("unsupported datatypes when generating fields")
		}
	SKIP:
		insertValues[colIndex] = gw
	}
}

var parentRowCounts = map[string]int64{}
var parentRowCountsMutex sync.Mutex

// parentRowCount counts a parent's rows once per run. A parent is fully
// inserted before anything pointing at it is, so the count does not move while
// it is being read from.
func parentRowCount(schema, table string) (int64, error) {
	parentRowCountsMutex.Lock()
	defer parentRowCountsMutex.Unlock()

	if count, ok := parentRowCounts[schema+"."+table]; ok {
		return count, nil
	}
	count, err := db.CountRows(schema, table)
	if err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, errors.Errorf("table %s.%s is empty, so there is nothing to point a foreign key at. Insert into it first, and check --rows-per-table if this run was meant to fill it", schema, table)
	}
	log.Debug().Str("table", table).Str("schema", schema).Int64("rows", count).Msg("counted the rows of a parent table")
	parentRowCounts[schema+"."+table] = count
	return count, nil
}

func (in *Insert) sampleConstraints(constraints db.Constraints, values [][]Getter) error {

	// subslice stores only a few columns grouped together with the FK columns
	subSlices := map[*db.Constraint][][]Getter{}
	colIdx := 0
	for _, constraint := range constraints {
		subSlice := make([][]Getter, len(values))
		for i := range subSlice {
			subSlice[i] = values[i][colIdx : colIdx+len(constraint.ReferencedFields)]
		}
		subSlices[constraint] = subSlice
		colIdx += len(constraint.ReferencedFields)
	}

	// The foreign keys that between them make up a unique key of this table
	// have to be filled together, or the key repeats however well each of them
	// behaves on its own column.
	shared, key := in.table.ConstraintsSharingAUniqueKey(constraints)

	for _, constraint := range constraints {
		if slices.Contains(shared, constraint) {
			continue
		}

		// Every sampler needs the size of the parent it reads from: it decides
		// how large a coin flip has to be to bring anything back, where the
		// sequential pager wraps around, and what row numbers the normal and
		// zipf laws may draw. Measured on --rows, the size of the table being
		// filled, all three were wrong for any parent of a different size,
		// which a dimension table always is.
		parentSize, err := parentRowCount(constraint.ReferencedTableSchema, constraint.ReferencedTableName)
		if err != nil {
			return err
		}

		samplerInit := in.fklinks.relationship(constraint.ReferencedTableName, in.table.Name)
		sampler := samplerInit(constraint.ReferencedFields, constraint.ReferencedTableSchema, constraint.ReferencedTableName, constraint.ConstraintName, subSlices[constraint], parentSize, &in.fklinks)

		// An imported dump may say how skewed this key was, which no sampler
		// knows about: it is a property of the relationship rather than of the
		// distribution the caller asked for, so it is layered on top.
		sampler = newSkewedSample(sampler, constraint, subSlices[constraint], parentSize, in.keyFrequenciesFor(constraint), &in.fklinks)

		if err := sampler.Sample(); err != nil {
			return errors.Wrap(err, "sampleFieldsTable")
		}
	}

	if len(shared) == 0 {
		return nil
	}
	sampler, err := in.newCompositeKeySample(shared, key, subSlices)
	if err != nil {
		return err
	}
	return errors.Wrap(sampler.Sample(), "sampleFieldsTable")
}
