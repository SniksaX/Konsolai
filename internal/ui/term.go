package ui

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

const statusDelay = 300 * time.Millisecond

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
	if !stdoutTTY() {
		return func() {}
	}
	var (
		mu    sync.Mutex
		shown bool
		once  sync.Once
	)
	stop := make(chan struct{})
	go func() {
		t := time.NewTimer(statusDelay)
		defer t.Stop()
		select {
		case <-t.C:
			mu.Lock()
			if !shown {
				fmt.Fprint(w, Dim(msg))
				shown = true
			}
			mu.Unlock()
		case <-stop:
		}
	}()
	return func() {
		once.Do(func() {
			close(stop)
			mu.Lock()
			if shown {
				fmt.Fprint(w, "\r\033[K")
			}
			mu.Unlock()
		})
	}
}
