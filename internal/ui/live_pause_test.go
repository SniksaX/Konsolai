package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestPauseLiveClearsTheSpinnerLine(t *testing.T) {
	var buf bytes.Buffer
	restore := TestHook(&buf, true, 5*time.Millisecond, time.Now)
	defer restore()
	testSpinnerWriter(&buf)
	live := StartLive("thinking")
	time.Sleep(40 * time.Millisecond)
	PauseLive()
	afterPause := buf.String()
	if !strings.Contains(afterPause, clearLine) {
		t.Fatalf("pause did not clear the line: %q", afterPause)
	}
	// While paused the spinner must not paint again.
	time.Sleep(40 * time.Millisecond)
	if buf.String() != afterPause {
		t.Fatal("the spinner painted while paused")
	}
	// The prompt never appears on a spinner row.
	if strings.Contains(afterPause, "this sends your prompts") {
		t.Fatal("unexpected prompt")
	}
	ResumeLive()
	time.Sleep(40 * time.Millisecond)
	if buf.String() == afterPause {
		t.Fatal("the spinner did not resume")
	}
	live.Stop()
}

// testSpinnerWriter keeps the spinner's test writer in one place.
func testSpinnerWriter(w *bytes.Buffer) {
	if hookWriter() != w {
		t := TestHook(w, true, 5*time.Millisecond, time.Now)
		_ = t
	}
}
