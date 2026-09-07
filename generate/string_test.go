package generate

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		max      int64
		expected string
	}{
		{name: "shorter than the limit", in: "Chad", max: 10, expected: "Chad"},
		{name: "exactly the limit", in: "Chad", max: 4, expected: "Chad"},
		{name: "ascii is cut where it would be", in: "Chad", max: 2, expected: "Ch"},
		{name: "a multi-byte rune is kept whole", in: "Åland Islands", max: 2, expected: "Ål"},
		// The observed failure: "Côte dIvoire" cut to two *bytes* is 0x43 0xc3,
		// and the closing quote of the INSERT then makes the 0xc3 0x27 postgres
		// complained about.
		{name: "the cut lands inside a later rune", in: "Côte dIvoire", max: 2, expected: "Cô"},
		{name: "cutting before a multi-byte rune", in: "Åland", max: 1, expected: "Å"},
		{name: "every rune is multi-byte", in: "日本語", max: 2, expected: "日本"},
		{name: "no limit given", in: "Åland Islands", max: 0, expected: "Åland Islands"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := truncateRunes(test.in, test.max)
			if got != test.expected {
				t.Errorf("truncateRunes(%q, %d) = %q, want %q", test.in, test.max, got, test.expected)
			}
			if !utf8.ValidString(got) {
				t.Errorf("truncateRunes(%q, %d) returned invalid utf8: % x", test.in, test.max, got)
			}
		})
	}
}

// A column too narrow for a country name gets the two-letter code instead of
// the first two bytes of a name, which used to end mid-rune.
func TestNewRandomStringNarrowCountry(t *testing.T) {
	for i := 0; i < 200; i++ {
		got := NewRandomString("country", 2).String()
		if !utf8.ValidString(got) {
			t.Fatalf("country column produced invalid utf8: % x", got)
		}
		if utf8.RuneCountInString(got) > 2 {
			t.Fatalf("country column produced %q, wider than the column", got)
		}
	}
}

// Every generator has to fit the column it fills without splitting a rune.
func TestNewRandomStringStaysValid(t *testing.T) {
	columns := []string{"country", "city", "first_name", "last_name", "email", "state", "company", "language", "color", "material", "description"}
	for _, column := range columns {
		for _, max := range []int64{1, 2, 3, 8} {
			for i := 0; i < 50; i++ {
				got := NewRandomString(column, max).String()
				if !utf8.ValidString(got) {
					t.Fatalf("column %q at width %d produced invalid utf8: % x", column, max, got)
				}
				if int64(utf8.RuneCountInString(got)) > max {
					t.Fatalf("column %q at width %d produced %q", column, max, got)
				}
				if strings.Contains(got, "'") {
					t.Fatalf("column %q produced an unescaped quote: %q", column, got)
				}
			}
		}
	}
}
