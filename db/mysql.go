package db

import (
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/pkg/errors"
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

func (_ MySQL) GetFields(schema, tablename string) ([]Field, error) {
	//                           +--------------------------- field type
	//                           |          +---------------- field size / enum values: decimal(10,2) or enum('a','b')
	//                           |          |     +---------- extra info (unsigned, etc)
	//                           |          |     |
	re := regexp.MustCompile(`^(.*?)(?:\((.*?)\)(.*))?$`)
	selectValues := []string{
		"COLUMN_NAME",
		"IS_NULLABLE",
		"DATA_TYPE",
		"CHARACTER_MAXIMUM_LENGTH",
		"NUMERIC_PRECISION",
		"NUMERIC_SCALE",
		"COLUMN_TYPE",
		"COLUMN_KEY",
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

	for rows.Next() {
		var f Field
		var allowNull, columnType string
		fields := makeScanRecipients(&f, &allowNull, &columnType, cols)
		err := rows.Scan(fields...)
		if err != nil {
			//log.Errorf("Cannot get table fields: %s", err)
			continue
		}

		allowedValues := []string{}
		if f.DataType == "enum" || f.DataType == "set" {
			m := re.FindStringSubmatch(columnType)
			if len(m) < 2 {
				continue
			}
			vals := strings.Split(m[2], ",")
			for _, val := range vals {
				val = strings.TrimPrefix(val, "'")
				val = strings.TrimSuffix(val, "'")
				allowedValues = append(allowedValues, val)
			}
		}

		f.SetEnumVals = allowedValues

		f.IsNullable = allowNull == "YES"
		fields = append(fields, f)

	}

	if rows.Err() != nil {
		return []Field{}, rows.Err()
	}
	return fields, nil
}

func makeScanRecipients(f *Field, allowNull, columnType *string, cols []string) []interface{} {
	fields := []interface{}{
		&f.ColumnName,
		&allowNull,
		&f.DataType,
		&f.CharacterMaximumLength,
		//&f.CharacterOctetLength,
		&f.NumericPrecision,
		&f.NumericScale,
		&columnType,
		&f.ColumnKey,
	}

	return fields
}

/*
func (_ MySQL) GetIndexes(schema, tableName string) (map[string]Index, error) {
	query := fmt.Sprintf("SHOW INDEXES FROM `%s`.`%s`", schema, tableName)
	rows, err := DB.Query(query)
	if err != nil {
		return nil, err
	}

	indexes := make(map[string]Index)

	for rows.Next() {
		var i mySQLIndexField
		var table string
		fields := []interface{}{&table, &i.NonUnique, &i.KeyName, &i.SeqInIndex,
			&i.ColumnName, &i.Collation, &i.Cardinality, &i.Null, &i.IndexType,
			&i.Comment, &i.IndexComment,
		}

		err = rows.Scan(fields...)
		if err != nil {
			return nil, fmt.Errorf("cannot read indexes: %s", err)
		}
		if index, ok := indexes[i.KeyName]; !ok {
			indexes[i.KeyName] = Index{
				Name:     i.KeyName,
				IsUnique: !i.NonUnique,
				Fields:   []string{i.ColumnName},
			}

		} else {
			index.Fields = append(index.Fields, i.ColumnName)
			index.IsUnique = index.IsUnique || !i.NonUnique
		}
	}
	if err := rows.Close(); err != nil {
		return nil, errors.Wrap(err, "Cannot close query rows at getIndexes")
	}

	return indexes, nil
}
*/
func (_ MySQL) GetConstraints(schema, tableName string) ([]Constraint, error) {
	query := "SELECT tc.CONSTRAINT_NAME, " +
		"kcu.COLUMN_NAME, " +
		"kcu.REFERENCED_TABLE_SCHEMA, " +
		"kcu.REFERENCED_TABLE_NAME, " +
		"kcu.REFERENCED_COLUMN_NAME " +
		"FROM information_schema.TABLE_CONSTRAINTS tc " +
		"LEFT JOIN information_schema.KEY_COLUMN_USAGE kcu " +
		"ON tc.CONSTRAINT_NAME = kcu.CONSTRAINT_NAME " +
		"WHERE tc.CONSTRAINT_TYPE = 'FOREIGN KEY' " +
		fmt.Sprintf("AND tc.TABLE_SCHEMA = '%s' ", schema) +
		fmt.Sprintf("AND tc.TABLE_NAME = '%s'", tableName)
	rows, err := DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	constraints := []Constraint{}

	for rows.Next() {
		var c Constraint
		err := rows.Scan(&c.ConstraintName, &c.ColumnName, &c.ReferencedTableSchema,
			&c.ReferencedTableName, &c.ReferencedColumnName)
		if err != nil {
			return nil, fmt.Errorf("cannot read constraints: %s", err)
		}
		constraints = append(constraints, c)
	}

	return constraints, nil
}

func (_ MySQL) InsertTemplate() string {
	return "INSERT IGNORE INTO %s.%s (%s) VALUES \n"
}

func (_ MySQL) Escape(s string) string {
	if strings.HasPrefix(s, "`") && strings.HasSuffix(s, "`") {
		return url.QueryEscape(s)
	}
	return "`" + url.QueryEscape(s) + "`"
}

func (_ MySQL) SetTableMetadata(database, tablename string) Table {
	return Table{Schema: database, Name: tablename}
}
