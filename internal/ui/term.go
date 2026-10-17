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

func Dim(s string) string    { return Current().Muted(s) }
func Bold(s string) string   { return Current().Strong(s) }
func Red(s string) string    { return Current().Error(s) }
func Green(s string) string  { return Current().Success(s) }
func Yellow(s string) string { return Current().Warn(s) }
func Cyan(s string) string   { return Current().Heading(s) }

func Status(w io.Writer, msg string) (clear func()) {
	_ = w
	return Start(msg)
}
