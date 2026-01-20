package db

import (
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

// indexField holds raw index information as defined in INFORMATION_SCHEMA table
type mySQLIndexField struct {
	KeyName     string
	SeqInIndex  int
	ColumnName  string
	Collation   sql.NullString
	Cardinality sql.NullInt64
	//SubPart      sql.NullInt64
	//Packed       sql.NullString
	Null         string
	IndexType    string
	Comment      string
	IndexComment string
	NonUnique    bool
	//Visible      string // MySQL 8.0+
	//Expression   sql.NullString // MySQL 8.0.16+
	//Clustered string // TiDB Support
}

func (mysql MySQL) GetFields(schema, tablename string) ([]Field, error) {
	selectValues := []string{
		"COLUMN_NAME",
		"IS_NULLABLE = 'YES'",
		"DATA_TYPE",
		"CHARACTER_MAXIMUM_LENGTH",
		"NUMERIC_PRECISION",
		"NUMERIC_SCALE",
		"COLUMN_TYPE",
		"COLUMN_KEY",
		"extra like '%auto_increment%'",
		"COLUMN_DEFAULT IS NOT NULL",
	}

	query := "SELECT " + strings.Join(selectValues, ",") +
		" FROM `information_schema`.`COLUMNS` " +
		"WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? " +
		"ORDER BY ORDINAL_POSITION"

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
	}

	return fields
}
func (_ MySQL) GetConstraints(schema, tableName string) ([]*Constraint, error) {
	query := `SELECT tc.CONSTRAINT_NAME,
			kcu.REFERENCED_TABLE_SCHEMA,
			kcu.REFERENCED_TABLE_NAME,
			group_concat(kcu.COLUMN_NAME ORDER BY ordinal_position SEPARATOR ';'),
			group_concat(kcu.REFERENCED_COLUMN_NAME ORDER BY ordinal_position SEPARATOR ';')
		FROM information_schema.TABLE_CONSTRAINTS tc
		LEFT JOIN information_schema.KEY_COLUMN_USAGE kcu
			ON tc.CONSTRAINT_NAME = kcu.CONSTRAINT_NAME
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
		c.ReferencedColumsName = strings.Split(refColumnsNameAgg, ";")
		constraints = append(constraints, &c)
	}

	return constraints, nil
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
