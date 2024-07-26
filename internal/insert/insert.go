package insert

import (
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

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

func (in *Insert) insert(count int64, dryRun bool) (int64, error) {
	if count < 1 {
		return 0, nil
	}
	values := make([]string, 0, count)
	insertQuery := generateInsertStmt(in.table)

	for i := int64(0); i < count; i++ {
		valueFns := makeValueFuncs(in.db, in.table.Fields, nil)
		values = append(values, valueFns.String())
	}

	insertQuery += strings.Join(values, ",\n")

	if dryRun {
		if _, err := in.writer.Write([]byte(insertQuery + "\n")); err != nil {
			return 0, err
		}
		return count, nil
	}

	res, err := in.db.Exec(insertQuery)
	if err != nil {
		fmt.Println(insertQuery)
		return 0, err
	}
	ra, _ := res.RowsAffected()
	return ra, err
}

func generateInsertStmt(table *db.Table) string {
	fields := getFieldNames(table.Fields)
	query := fmt.Sprintf(db.InsertTemplate(), //nolint
		db.Escape(table.Schema),
		db.Escape(table.Name),
		strings.Join(fields, ","),
	)
	return query
}

func getFieldNames(fields []db.Field) []string {
	fieldNames := make([]string, 0, len(fields))

	for _, field := range fields {
		if !isSupportedType(field.DataType) {
			continue
		}
		if !field.IsNullable && field.ColumnKey == "PRI" && field.AutoIncrement {
			continue
		}
		fieldNames = append(fieldNames, db.Escape(field.ColumnName))
	}
	return fieldNames
}

func isSupportedType(fieldType string) bool {
	supportedTypes := map[string]bool{
		"tinyint":    true,
		"smallint":   true,
		"mediumint":  true,
		"int":        true,
		"integer":    true,
		"bigint":     true,
		"float":      true,
		"decimal":    true,
		"double":     true,
		"char":       true,
		"varchar":    true,
		"date":       true,
		"datetime":   true,
		"timestamp":  true,
		"time":       true,
		"year":       true,
		"tinyblob":   true,
		"tinytext":   true,
		"blob":       true,
		"text":       true,
		"mediumblob": true,
		"mediumtext": true,
		"longblob":   true,
		"longtext":   true,
		"binary":     true,
		"varbinary":  true,
		"enum":       true,
		"set":        true,
		"bool":       true,
		"boolean":    true,
	}
	_, ok := supportedTypes[fieldType]
	return ok
}

func makeValueFuncs(conn *sql.DB, fields []db.Field, cg map[string]string) insertValues {
	var values []getters.Getter
	for _, field := range fields {
		if !field.IsNullable && field.ColumnKey == "PRI" && field.AutoIncrement {
			continue
		}
		if field.Constraint != nil {
			samples, err := getSamples(conn, field.Constraint.ReferencedTableSchema,
				field.Constraint.ReferencedTableName,
				field.Constraint.ReferencedColumnName,
				100, field.DataType)
			if err != nil {
				log.Printf("cannot get samples for field %q: %s\n", field.ColumnName, err)
				continue
			}
			values = append(values, getters.NewRandomSample(field.ColumnName, samples, field.IsNullable))
			continue
		}
		maxValue := maxValues["bigint"]
		if m, ok := maxValues[field.DataType]; ok {
			maxValue = m
		}
		switch field.DataType {
		case "tinyint", "bit", "bool", "boolean":
			values = append(values, getters.NewRandomIntRange(field.ColumnName, 0, 1, field.IsNullable))
		case "smallint", "mediumint", "int", "integer", "bigint":
			values = append(values, getters.NewRandomInt(field.ColumnName, maxValue, field.IsNullable))
		case "float", "decimal", "double", "numeric":
			values = append(values, getters.NewRandomDecimal(field.ColumnName, field.NumericPrecision.Int64, field.NumericScale.Int64, field.IsNullable))
		case "char", "varchar":
			values = append(values, getters.NewRandomString(field.ColumnName,
				field.CharacterMaximumLength.Int64, field.IsNullable))
		case "date":
			values = append(values, getters.NewRandomDate(field.ColumnName, field.IsNullable))
		case "datetime", "timestamp":
			values = append(values, getters.NewRandomDateTime(field.ColumnName, field.IsNullable))
		case "tinyblob", "tinytext", "blob", "text", "mediumtext", "mediumblob", "longblob", "longtext":
			values = append(values, getters.NewRandomString(field.ColumnName,
				field.CharacterMaximumLength.Int64, field.IsNullable))
		case "time":
			values = append(values, getters.NewRandomTime(field.IsNullable))
		case "year":
			values = append(values, getters.NewRandomIntRange(field.ColumnName, int64(time.Now().Year()-1),
				int64(time.Now().Year()), field.IsNullable))
		case "enum", "set":
			values = append(values, getters.NewRandomEnum(field.SetEnumVals, field.IsNullable))
		case "binary", "varbinary":
			values = append(values, getters.NewRandomBinary(field.ColumnName, field.CharacterMaximumLength.Int64, field.IsNullable))
		default:
			log.Printf("cannot get field type: %s: %s\n", field.ColumnName, field.DataType)
		}
	}

	return values
}

var storedSampleCount = map[string]int64{}

func getSamples(conn *sql.DB, schema, table, field string, samples int64, dataType string) ([]interface{}, error) {
	var count int64
	var query string

	count, ok := storedSampleCount[schema+"#"+table]
	if !ok {
		queryCount := fmt.Sprintf("SELECT COUNT(*) FROM %s.%s", db.Escape(schema), db.Escape(table))
		if err := conn.QueryRow(queryCount).Scan(&count); err != nil {
			return nil, fmt.Errorf("cannot get count for table %q: %s", table, err)
		}
		storedSampleCount[schema+"#"+table] = count
	}

	if count < samples {
		query = fmt.Sprintf("SELECT %s FROM %s.%s", db.Escape(field), db.Escape(schema), db.Escape(table))
	} else {
		query = fmt.Sprintf("SELECT %s FROM %s.%s WHERE RAND() <= .3 LIMIT %d",
			db.Escape(field), db.Escape(schema), db.Escape(table), samples)
	}

	rows, err := conn.Query(query)
	if err != nil {
		return nil, fmt.Errorf("cannot get samples: %s, %s", query, err)
	}
	defer rows.Close()

	values := []interface{}{}

	for rows.Next() {
		var err error
		var val interface{}

		switch dataType {
		case "tinyint", "smallint", "mediumint", "int", "integer", "bigint", "year":
			var v int64
			err = rows.Scan(&v)
			val = v
		case "char", "varchar", "blob", "text", "mediumtext",
			"mediumblob", "longblob", "longtext":
			var v string
			err = rows.Scan(&v)
			val = v
		case "binary", "varbinary":
			var v []rune
			err = rows.Scan(&v)
			val = v
		case "float", "decimal", "double":
			var v float64
			err = rows.Scan(&v)
			val = v
		case "date", "time", "datetime", "timestamp":
			var v time.Time
			err = rows.Scan(&v)
			val = v
		}
		if err != nil {
			return nil, fmt.Errorf("cannot scan sample: %s", err)
		}
		values = append(values, val)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cannot get samples: %s", err)
	}
	return values, nil
}
