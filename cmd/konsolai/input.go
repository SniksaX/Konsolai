package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/SniksaX/Konsolai/internal/ui"
)

const (
	stdinRequestCap = 64 << 10
	stdinContextCap = 32 << 10
)

var (
	stdinIsTTY = defaultStdinTTY
	readRawTTY = defaultReadTTY
	readStdinN = defaultReadStdin
)

func defaultStdinTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func stdoutIsTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func defaultReadTTY() (string, error) {
	fmt.Fprintln(os.Stderr, ui.Dim("› type your request, Ctrl+D to send"))
	f, err := os.Open("/dev/tty")
	if err != nil {
		return "", fmt.Errorf("konsolai: tty: %w", err)
	}
	defer f.Close()
	return readLimited(f, stdinRequestCap)
}

func defaultReadStdin(max int) (string, error) {
	return readLimited(os.Stdin, max)
}

func readLimited(r io.Reader, max int) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, int64(max)+1))
	if err != nil {
		return "", err
	}
	note := ""
	if len(b) > max {
		b = b[:max]
		note = "\n[stdin truncated]"
	}
	return string(b) + note, nil
}

func resolveRequest(inv invocation) (invocation, error) {
	tty := stdinIsTTY()
	if strings.TrimSpace(inv.Request) == "" {
		var raw string
		var err error
		if tty {
			raw, err = readRawTTY()
		} else {
			raw, err = readStdinN(stdinRequestCap)
		}
		if err != nil {
			return inv, err
		}
		inv.Request = strings.TrimRight(raw, "\n")
		if strings.TrimSpace(inv.Request) == "" {
			return inv, errRequest
		}
		return inv, nil
	}
	if !tty {
		extra, err := readStdinN(stdinContextCap)
		if err != nil {
			return inv, err
		}
		extra = strings.TrimRight(extra, "\n")
		if extra != "" {
			inv.Request = inv.Request + "\n\n<stdin>\n" + extra + "\n</stdin>"
		}
	}
	return inv, nil
}
