package cmd

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

// go test hands the process files and pipes, never a terminal, so this is the
// branch a redirected run takes: no escape sequence and no time, only the
// level, the message and its fields.
func TestConsoleLogOffATerminalIsPlain(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// TestMain silences logging for the package
	level := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	defer zerolog.SetGlobalLevel(level)

	logger := zerolog.New(ConsoleLog(f)).With().Timestamp().Logger()
	logger.Warn().Str("table", "orders").Msg("cannot be spread over exactly 8045 pages")

	out, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("a redirected log carries escape sequences: %q", got)
	}
	if regexp.MustCompile(`\d{1,2}:\d{2}`).MatchString(got) {
		t.Errorf("a redirected log carries the time: %q", got)
	}
	if want := "WRN cannot be spread over exactly 8045 pages table=orders\n"; got != want {
		t.Errorf("ConsoleLog() wrote %q, want %q", got, want)
	}
}

func TestOnATerminalIsFalseForAPipeAndAFile(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if onATerminal(w) {
		t.Error("a pipe is not a terminal")
	}

	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if onATerminal(f) {
		t.Error("a regular file is not a terminal")
	}
}
