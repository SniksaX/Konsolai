package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("KONSOLAI_PTY_CHILD") != "" {
		os.Exit(m.Run())
	}
	real := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		fmt.Fprintln(real, err)
		os.Exit(1)
	}
	os.Stdout = w
	code := m.Run()
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()
	os.Stdout = real
	if bytes.Contains(buf.Bytes(), []byte{0x1b}) {
		fmt.Fprintln(real, "os.Stdout received an escape sequence")
		if code == 0 {
			code = 1
		}
	}
	_, _ = real.Write(buf.Bytes())
	os.Exit(code)
}
