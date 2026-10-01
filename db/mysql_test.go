package db

import "testing"

// ROW_NUMBER() came with MySQL 8.0 and MariaDB 10.2. Anything older, or a
// version string that cannot be read, numbers rows with a user variable,
// which every version runs.
func TestVersionHasWindowFunctions(t *testing.T) {
	tests := map[string]bool{
		"5.7.44":                false,
		"5.7.44-log":            false,
		"8.0.36":                true,
		"8.4.3":                 true,
		"9.1.0":                 true,
		"5.5.5-10.1.48-MariaDB": false,
		"5.5.5-10.6.12-MariaDB": true,
		"10.1.48-MariaDB":       false,
		"10.2.44-MariaDB":       true,
		"11.4.2-MariaDB-log":    true,
		"":                      false,
		"unknown":               false,
	}
	for version, want := range tests {
		if got := versionHasWindowFunctions(version); got != want {
			t.Errorf("versionHasWindowFunctions(%q) = %v, want %v", version, got, want)
		}
	}
}
