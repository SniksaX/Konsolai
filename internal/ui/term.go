package ui

import (
	"io"
	"os"
)

func stdoutTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func Dim(s string) string { return Current().Muted(s) }

func Status(w io.Writer, msg string) (clear func()) {
	_ = w
	return Start(msg)
}
