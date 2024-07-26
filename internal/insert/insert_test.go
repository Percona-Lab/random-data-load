package insert

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/ylacancellera/random-data-load/internal/tu"
	"github.com/ylacancellera/random-data-load/tableparser"
)

func TestBasic(t *testing.T) {
	db := tu.GetMySQLConnection(t)
	tu.LoadQueriesFromFile(t, "child.sql")

	table, err := tableparser.New(db, "test", "parent")
	assert.NoError(t, err)

	i := New(db, table)

	n, err := i.DryRun(9, 5)
	assert.NoError(t, err)
	assert.Equal(t, int64(9), n)

	n, err = i.Run(9, 5)
	assert.NoError(t, err)
	assert.Equal(t, int64(9), n)
}
