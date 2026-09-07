package generate

import (
	"encoding/json"
	"math/rand"
	"strings"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/rs/zerolog/log"
)

// RandomJSON getter
//
// A json column left out of the INSERT is a column of NULLs, and its width is
// missing from every row: a document is usually the widest thing a row holds,
// which is exactly what a plan-fidelity reproduction is measuring. So a small
// document is generated instead. It carries no resemblance to the one
// production stores, only a comparable shape.
type RandomJSON struct {
	value string
}

func (r *RandomJSON) String() string {
	return r.value
}

func (r *RandomJSON) IsQuotable() bool {
	return true
}

func NewRandomJSON(name string) *RandomJSON {
	document := map[string]interface{}{
		strings.ToLower(name): gofakeit.ID(),
		"generated_by":        "random-data-load",
		"n":                   rand.Int63n(0x7FFFFFFF),
		"tags":                []string{gofakeit.ProductMaterial(), gofakeit.Color()},
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		// Nothing in the document can fail to encode, but an empty object is
		// still valid json and keeps the insert going.
		log.Debug().Err(err).Msg("cannot encode a generated json document")
		return &RandomJSON{"{}"}
	}

	// The escaping of the literal is left to the engine, but a single quote
	// inside a generated word would be doubled and land in the document, so it
	// is dropped like NewRandomString drops it.
	return &RandomJSON{strings.ReplaceAll(string(encoded), "'", "")}
}
