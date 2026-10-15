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

func wrap(code, s string) string {
	if !stdoutTTY() {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func Dim(s string) string    { return wrap("2", s) }
func Bold(s string) string   { return wrap("1", s) }
func Red(s string) string    { return wrap("31", s) }
func Green(s string) string  { return wrap("32", s) }
func Yellow(s string) string { return wrap("33", s) }
func Cyan(s string) string   { return wrap("36", s) }

func Status(w io.Writer, msg string) (clear func()) {
	_ = w
	return Start(msg)
}
