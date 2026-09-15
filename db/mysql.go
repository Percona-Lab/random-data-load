package db

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

type MySQL struct{}

func (_ MySQL) Connect(dbInfo Config) (*sql.DB, error) {
	netType := "tcp"
	if dbInfo.Port == 0 {
		dbInfo.Port = 3306
	}
	address := net.JoinHostPort(dbInfo.Host, fmt.Sprintf("%d", dbInfo.Port))

	if dbInfo.Host == "localhost" {
		netType = "unix"
		address = dbInfo.Host
	}

	cfg := &mysql.Config{
		User:                    dbInfo.User,
		Passwd:                  dbInfo.Password,
		Net:                     netType,
		Addr:                    address,
		DBName:                  dbInfo.Database,
		AllowCleartextPasswords: true,
		AllowNativePasswords:    true,
		AllowOldPasswords:       true,
		CheckConnLiveness:       true,
		ParseTime:               true,
	}

	return sql.Open("mysql", cfg.FormatDSN())
}

func (mysql MySQL) GetFields(schema, tablename string) ([]Field, error) {

	query := `SELECT 
		COLUMN_NAME,
		IS_NULLABLE = 'YES',
		DATA_TYPE,
		CHARACTER_MAXIMUM_LENGTH,
		NUMERIC_PRECISION,
		NUMERIC_SCALE,
		COLUMN_TYPE,
		COLUMN_KEY,
		extra like '%auto_increment%',
		COLUMN_DEFAULT IS NOT NULL,
		extra like '%VIRTUAL%'
	FROM information_schema.COLUMNS
	WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? 
	ORDER BY ORDINAL_POSITION`

	rows, err := DB.Query(query, schema, tablename)
	if err != nil {
		return []Field{}, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return []Field{}, errors.Wrap(err, "Cannot get column names")
	}

	var fields = []Field{}
	var found bool

	for rows.Next() {
		found = true

		var f Field
		var columnType string
		scanRecipients := mysql.makeScanRecipients(&f, &columnType, cols)
		err := rows.Scan(scanRecipients...)
		if err != nil {
			log.Error().Err(err).Msg("cannot get fields")
			continue
		}

		allowedValues := []string{}
		if f.DataType == "enum" || f.DataType == "set" {
			columnType, ok := strings.CutSuffix(columnType, ")")
			if !ok {
				log.Error().Str("columnType", columnType).Msg("unexpected columnType, suffix ) not found")
				continue
			}
			columnType, ok = strings.CutPrefix(columnType, f.DataType+"(")
			if !ok {
				log.Error().Str("columnType", columnType).Str("prefix", f.DataType+"(").Msg("unexpected columnType, prefix not found")
				continue
			}
			vals := strings.Split(columnType, ",")
			for _, val := range vals {
				val = strings.TrimPrefix(val, "'")
				val = strings.TrimSuffix(val, "'")
				allowedValues = append(allowedValues, val)
			}
		}

		f.SetEnumVals = allowedValues

		fields = append(fields, f)

	}

	if rows.Err() != nil {
		return []Field{}, rows.Err()
	}

	if !found {
		return []Field{}, errors.Wrapf(ErrFieldsNotFound, "query: %s", query)
	}
	return fields, nil
}

func (_ MySQL) makeScanRecipients(f *Field, columnType *string, cols []string) []interface{} {
	fields := []interface{}{
		&f.ColumnName,
		&f.IsNullable,
		&f.DataType,
		&f.CharacterMaximumLength,
		//&f.CharacterOctetLength,
		&f.NumericPrecision,
		&f.NumericScale,
		columnType,
		&f.ColumnKey,
		&f.AutoIncrement,
		&f.HasDefaultValue,
		&f.IsGenerated,
	}

	return fields
}
func (_ MySQL) GetConstraints(schema, tableName string) ([]*Constraint, error) {
	// A constraint is only identified by its name together with the table
	// holding it: names are unique within a schema, but the same name is
	// expected to come back in every schema holding a copy of that table, so
	// matching on the name alone aggregates the columns of every namesake and
	// lists them all under this table's constraint.
	query := `SELECT tc.CONSTRAINT_NAME,
			kcu.REFERENCED_TABLE_SCHEMA,
			kcu.REFERENCED_TABLE_NAME,
			group_concat(kcu.COLUMN_NAME ORDER BY kcu.ORDINAL_POSITION SEPARATOR ';'),
			group_concat(kcu.REFERENCED_COLUMN_NAME ORDER BY kcu.ORDINAL_POSITION SEPARATOR ';')
		FROM information_schema.TABLE_CONSTRAINTS tc
		JOIN information_schema.KEY_COLUMN_USAGE kcu
			ON kcu.CONSTRAINT_SCHEMA = tc.CONSTRAINT_SCHEMA
			AND kcu.CONSTRAINT_NAME = tc.CONSTRAINT_NAME
			AND kcu.TABLE_SCHEMA = tc.TABLE_SCHEMA
			AND kcu.TABLE_NAME = tc.TABLE_NAME
		JOIN information_schema.tables t
			ON kcu.referenced_table_schema = t.table_schema
			AND kcu.referenced_table_name = t.table_name
		WHERE tc.CONSTRAINT_TYPE = 'FOREIGN KEY'
			AND tc.TABLE_SCHEMA = ?
			AND tc.TABLE_NAME = ?
		GROUP BY 1,2,3`

	rows, err := DB.Query(query, schema, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	constraints := []*Constraint{}

	for rows.Next() {
		var c Constraint
		var columnsNameAgg, refColumnsNameAgg string
		err := rows.Scan(&c.ConstraintName, &c.ReferencedTableSchema,
			&c.ReferencedTableName, &columnsNameAgg, &refColumnsNameAgg)
		if err != nil {
			return nil, fmt.Errorf("cannot read constraints: %s", err)
		}
		c.ColumnsName = strings.Split(columnsNameAgg, ";")
		c.ReferencedColumnsName = strings.Split(refColumnsNameAgg, ";")
		constraints = append(constraints, &c)
	}

	return constraints, nil
}

// TruncateTables empties the tables one at a time, innermost first.
//
// mysql has no multi-table TRUNCATE and refuses to truncate a table a foreign
// key points at, so the checks are held off for the batch. They are restored
// on the same connection they were disabled on, since the setting is per
// session and the pool hands out connections freely.
func (mysql MySQL) TruncateTables(tables []*Table) error {
	ctx := context.Background()
	conn, err := DB.Conn(ctx)
	if err != nil {
		return errors.Wrap(err, "truncating: taking a connection")
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=0"); err != nil {
		return errors.Wrap(err, "truncating: disabling foreign key checks")
	}
	defer func() {
		if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=1"); err != nil {
			log.Error().Err(err).Msg("could not restore foreign_key_checks on the truncating connection")
		}
	}()

	// children first, so the order still reads correctly to anyone watching
	for i := len(tables) - 1; i >= 0; i-- {
		name := mysql.Escape(tables[i].Schema) + "." + mysql.Escape(tables[i].Name)
		query := "TRUNCATE TABLE " + name
		log.Debug().Str("query", query).Msg("emptying the tables this run fills")
		if _, err := conn.ExecContext(ctx, query); err != nil {
			return errors.Wrapf(err, "truncating %s", name)
		}
	}
	return nil
}

func (_ MySQL) InsertTemplate() string {
	return "INSERT INTO %s.%s (%s) VALUES \n"
}

func (_ MySQL) Escape(s string) string {
	if strings.HasPrefix(s, "`") && strings.HasSuffix(s, "`") {
		return s
	}
	return "`" + s + "`"
}

// EscapeValue doubles single quotes, and backslashes too: unlike postgres,
// mysql reads a backslash as an escape character inside a string literal
// unless NO_BACKSLASH_ESCAPES is set.
func (_ MySQL) EscapeValue(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\\", "\\\\"), "'", "''")
}

func (_ MySQL) SetTableMetadata(table *Table, database, tablename string) {
	table.Schema = database
	table.Name = tablename
}

func (_ MySQL) BinomialWhereClause(freqPercent float64) string {
	freq := fmt.Sprintf("%.10f", freqPercent/100)
	return "WHERE rand() < " + freq
}

func (_ MySQL) ErrShouldRetryTx(err error) bool {
	return strings.Contains(err.Error(), "Duplicate entry")
}

func (_ MySQL) FilterOnRowNumberFromClause(_ []Field, table, schema string) string {
	return fmt.Sprintf("%s.%s, (SELECT @rownumber := 0) f", Escape(schema), Escape(table))
}

func (_ MySQL) FilterOnRowNumberVarClause() string {
	return "(@rownumber := @rownumber + 1)"
}

// ValueTimeLayout leaves the offset out: DATETIME holds no time zone, and only
// 8.0.19 and above accept one in a literal at all. The driver hands the value
// over in the location it will read it back in, so writing it as it stands
// round-trips.
func (_ MySQL) ValueTimeLayout() string {
	return "2006-01-02 15:04:05.999999"
}

// Analyze recomputes the index statistics the optimizer reads.
//
// ANALYZE TABLE answers with a result set rather than an affected-row count,
// so it is run as a query and drained.
func (mysql MySQL) Analyze(schema, table string) error {
	query := "ANALYZE TABLE " + mysql.Escape(schema) + "." + mysql.Escape(table)
	log.Debug().Str("query", query).Msg("collecting statistics")
	rows, err := DB.Query(query)
	if err != nil {
		return errors.Wrapf(err, "analyzing %s.%s", schema, table)
	}
	defer rows.Close()
	for rows.Next() { //nolint
	}
	return errors.Wrapf(rows.Err(), "analyzing %s.%s", schema, table)
}

// TableStorage reads what information_schema says the table takes.
//
// InnoDB keeps no page count of its own, so it is the clustered index's size
// divided by the page size, and every figure here is an estimate sampled from
// a few index pages rather than a count. It is the closest mysql gets to
// postgres' relpages, and it is worth saying that it is not the same thing.
func (mysql MySQL) TableStorage(schema, table string) (Storage, error) {
	query := `SELECT coalesce(DATA_LENGTH, 0) DIV @@innodb_page_size,
		coalesce(TABLE_ROWS, 0),
		coalesce(AVG_ROW_LENGTH, 0)
	FROM information_schema.TABLES
	WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`

	var storage Storage
	err := DB.QueryRow(query, schema, table).Scan(&storage.Pages, &storage.Tuples, &storage.Width)
	if err != nil {
		return storage, errors.Wrapf(err, "reading the storage of %s.%s", schema, table)
	}
	storage.Note = "InnoDB samples these figures rather than counting them, so they move between reads"
	return storage, nil
}
