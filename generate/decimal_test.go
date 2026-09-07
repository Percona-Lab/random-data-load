package generate

import (
	"database/sql"
	"math/big"
	"strings"
	"testing"
)

func exact(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }

var unset = sql.NullInt64{}

// Every draw has to be a value the column would accept: at most p significant
// digits with s of them after the point, so strictly below 10^(p-s).
func TestNewRandomDecimalFitsTheColumn(t *testing.T) {
	tests := []struct{ precision, scale int64 }{
		{3, 2}, {5, 2}, {12, 2}, {5, 5}, {5, 0}, {10, 4}, {1, 0}, {38, 10}, {1000, 500},
	}

	for _, test := range tests {
		limit := new(big.Float).SetFloat64(1)
		limit.SetString("1" + strings.Repeat("0", int(test.precision-test.scale)))

		for i := 0; i < 500; i++ {
			got := NewRandomDecimal(exact(test.precision), exact(test.scale)).String()

			value, ok := new(big.Float).SetString(got)
			if !ok {
				t.Fatalf("numeric(%d,%d) produced %q, which is not a number", test.precision, test.scale, got)
			}
			if value.Cmp(limit) >= 0 {
				t.Fatalf("numeric(%d,%d) produced %q, out of range", test.precision, test.scale, got)
			}
			if _, frac, found := strings.Cut(got, "."); found && int64(len(frac)) > test.scale {
				t.Fatalf("numeric(%d,%d) produced %q, too many decimals", test.precision, test.scale, got)
			}
		}
	}
}

// float, double precision and real report a binary precision and no scale.
// Reading that precision as a digit count would be wrong, so they keep the
// approximate value they always had.
func TestNewRandomDecimalWithoutScale(t *testing.T) {
	for _, precision := range []sql.NullInt64{unset, exact(53), exact(24)} {
		for i := 0; i < 100; i++ {
			got := NewRandomDecimal(precision, unset).String()
			if _, ok := new(big.Float).SetString(got); !ok {
				t.Fatalf("precision %v produced %q, which is not a number", precision, got)
			}
		}
	}
}

func TestNewRandomDecimalIsNotQuoted(t *testing.T) {
	if NewRandomDecimal(exact(12), exact(2)).IsQuotable() {
		t.Error("a decimal must not be quoted in an INSERT")
	}
}
