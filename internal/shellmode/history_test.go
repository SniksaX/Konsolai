package shellmode

import (
	"strings"
	"testing"
)

func TestCapHeadTail(t *testing.T) {
	small := strings.Repeat("a", 100)
	if CapHeadTail(small, captureCap) != small {
		t.Fatal("small changed")
	}
	big := strings.Repeat("H", 40*1024) + strings.Repeat("T", 40*1024)
	got := CapHeadTail(big, captureCap)
	if len(got) >= len(big) {
		t.Fatalf("len %d", len(got))
	}
	if !strings.HasPrefix(got, strings.Repeat("H", 100)) {
		t.Fatal("head")
	}
	if !strings.HasSuffix(got, strings.Repeat("T", 100)) {
		t.Fatal("tail")
	}
	if !strings.Contains(got, truncateMark) {
		t.Fatal("mark")
	}
}

func TestCapWriterLimit(t *testing.T) {
	var sink strings.Builder
	w := newCapWriter(&sink, 64)
	head := strings.Repeat("A", 40)
	mid := strings.Repeat("M", 100)
	tail := strings.Repeat("Z", 40)
	_, _ = w.Write([]byte(head + mid + tail))
	got := w.String()
	if !strings.Contains(got, "A") || !strings.Contains(got, "Z") {
		t.Fatalf("%q", got)
	}
	if len(got) > 64+len(truncateMark) {
		t.Fatalf("len %d %q", len(got), got)
	}
	if sink.Len() != len(head+mid+tail) {
		t.Fatalf("sink %d", sink.Len())
	}
}

func TestHistoryInjectAndCap(t *testing.T) {
	var h History
	h.Add("echo secret ghp_abcdefghijklmnopqrstuvwxyz012345", strings.Repeat("x", 8000))
	if h.Len() != 1 {
		t.Fatal(h.Len())
	}
	if len(h.Items()[0].Output) > contextCap {
		t.Fatalf("out %d", len(h.Items()[0].Output))
	}
	if strings.Contains(h.Items()[0].Cmd, "ghp_") {
		t.Fatalf("not redacted %q", h.Items()[0].Cmd)
	}
	if !strings.Contains(h.Format(), "<shell_history>") || !strings.Contains(h.Format(), "echo secret") {
		t.Fatalf("%q", h.Format())
	}
	for i := 0; i < 8; i++ {
		h.Add("cmd", "out")
	}
	if h.Len() != maxKept {
		t.Fatalf("kept %d", h.Len())
	}
	got := h.Format()
	if strings.Count(got, "<shell_history>") != maxKept {
		t.Fatalf("%q", got)
	}
	h.Clear()
	if h.Len() != 0 || h.Format() != "" {
		t.Fatal("clear")
	}
}
