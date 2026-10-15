package tools

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestLineLimiterUnderLimit(t *testing.T) {
	var buf bytes.Buffer
	l := NewLineLimiter(&buf)
	if _, err := l.Write([]byte("a\nb\nc\n")); err != nil {
		t.Fatal(err)
	}
	l.Close(&buf)
	if got := buf.String(); got != "a\nb\nc\n" {
		t.Fatalf("got %q", got)
	}
}

func TestLineLimiterExactLimit(t *testing.T) {
	var buf bytes.Buffer
	l := NewLineLimiter(&buf)
	var in strings.Builder
	for i := range lineLimit {
		fmt.Fprintf(&in, "L%d\n", i)
	}
	if _, err := l.Write([]byte(in.String())); err != nil {
		t.Fatal(err)
	}
	var notice bytes.Buffer
	l.Close(&notice)
	if got := buf.String(); got != in.String() {
		t.Fatalf("passthrough mismatch len=%d want=%d", len(got), len(in.String()))
	}
	if notice.Len() != 0 {
		t.Fatalf("unexpected notice %q", notice.String())
	}
}

func TestLineLimiterOverflowCount(t *testing.T) {
	const extra = 7
	var buf bytes.Buffer
	l := NewLineLimiter(&buf)
	var in strings.Builder
	for i := range lineLimit + extra {
		fmt.Fprintf(&in, "L%d\n", i)
	}
	if _, err := l.Write([]byte(in.String())); err != nil {
		t.Fatal(err)
	}
	var notice bytes.Buffer
	l.Close(&notice)
	wantShown := 0
	for i := range lineLimit {
		wantShown += len(fmt.Sprintf("L%d\n", i))
	}
	if buf.Len() != wantShown {
		t.Fatalf("shown %d bytes, want %d", buf.Len(), wantShown)
	}
	if !strings.Contains(notice.String(), fmt.Sprintf("%d more lines hidden", extra)) {
		t.Fatalf("notice %q", notice.String())
	}
}

func TestLineLimiterPartialChunks(t *testing.T) {
	var buf bytes.Buffer
	l := NewLineLimiter(&buf)
	for _, chunk := range []string{"hel", "lo\nwor", "ld\n"} {
		if _, err := l.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	l.Close(&buf)
	if got := buf.String(); got != "hello\nworld\n" {
		t.Fatalf("got %q", got)
	}
}
