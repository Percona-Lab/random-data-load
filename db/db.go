package db

import (
	"database/sql"
	"errors"
)

type Config struct {
	Engine   string `enum:"mysql,pg" required:""`
	Database string
	Host     string
	User     string
	Password string
	Port     int
}

var (
	DB     *sql.DB
	engine Engine
)

type Engine interface {
	Connect(Config) (*sql.DB, error)
	GetFields(string, string) ([]Field, error)
	GetConstraints(string, string) ([]Constraint, error)
	InsertTemplate() string
	Escape(string) string
	SetTableMetadata(string, string) Table
}

func Connect(config Config) (*sql.DB, error) {
	err := setEngine(config)
	if err != nil {
		return nil, err
	}
	DB, err = engine.Connect(config)
	return DB, err
}

func setEngine(config Config) error {
	switch config.Engine {
	case "mysql":
		engine = MySQL{}
		return nil
	case "pg":
		engine = Postgres{}
		return nil
	default:
		return errors.New("unsupported engine")
	}
}

func GetFields(schema, table string) ([]Field, error) {
	return engine.GetFields(schema, table)
}

func GetConstraints(schema, table string) ([]Constraint, error) {
	return engine.GetConstraints(schema, table)
}

func InsertTemplate() string {
	return engine.InsertTemplate()
}

func Escape(s string) string {
	return engine.Escape(s)
}
