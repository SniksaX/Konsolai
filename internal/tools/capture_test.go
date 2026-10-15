package tools

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestCaptureUnderCap(t *testing.T) {
	var c Capture
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if got := c.String(); got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestCaptureExactCap(t *testing.T) {
	var c Capture
	in := bytes.Repeat([]byte("a"), captureWindow)
	if _, err := c.Write(in); err != nil {
		t.Fatal(err)
	}
	if got := c.String(); got != string(in) {
		t.Fatalf("len=%d want %d", len(got), len(in))
	}
}

func TestCaptureOverflow(t *testing.T) {
	var c Capture
	first := bytes.Repeat([]byte("A"), captureWindow)
	mid := bytes.Repeat([]byte("B"), 100)
	last := bytes.Repeat([]byte("C"), captureWindow)
	if _, err := c.Write(first); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(mid); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(last); err != nil {
		t.Fatal(err)
	}
	got := c.String()
	if !strings.HasPrefix(got, string(first)) {
		t.Fatal("missing first window")
	}
	if !strings.HasSuffix(got, string(last)) {
		t.Fatal("missing last window")
	}
	note := fmt.Sprintf("\n... [%d bytes truncated] ...\n", len(mid))
	if !strings.Contains(got, note) {
		t.Fatalf("missing truncation note %q in %q", note, got)
	}
	if strings.Contains(got, "B") {
		t.Fatal("middle bytes should be truncated")
	}
}
