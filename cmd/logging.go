package cmd

import (
	"os"

	"github.com/mattn/go-isatty"
	"github.com/rs/zerolog"
)

// ConsoleLog is the log writer for f. Colours and a wall-clock time help
// someone watching a terminal. Once f is redirected to a file or a pipe,
// nobody is watching: the colour codes land as literal escape sequences, and
// the time repeats itself on every line of a run that lasts minutes. Both are
// left out then.
func ConsoleLog(f *os.File) zerolog.ConsoleWriter {
	w := zerolog.ConsoleWriter{Out: f}
	if !onATerminal(f) {
		w.NoColor = true
		w.PartsExclude = []string{zerolog.TimestampFieldName}
	}
	return w
}

func onATerminal(f *os.File) bool {
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}
