package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestSpinnerFramesOnFakeTTY(t *testing.T) {
	var buf bytes.Buffer
	restore := TestHook(&buf, true, 5*time.Millisecond, nil)
	defer restore()
	stop := Start("thinking")
	time.Sleep(20 * time.Millisecond)
	stop()
	got := buf.String()
	if !strings.Contains(got, hideCursor) {
		t.Fatalf("missing hide cursor: %q", got)
	}
	if !strings.Contains(got, "thinking") {
		t.Fatalf("missing label: %q", got)
	}
	found := false
	for _, f := range braille {
		if strings.Contains(got, f) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing braille: %q", got)
	}
	if !strings.Contains(got, showCursor) {
		t.Fatalf("missing show cursor: %q", got)
	}
}

func TestSpinnerSkippedWhenNotTTY(t *testing.T) {
	var buf bytes.Buffer
	restore := TestHook(&buf, false, 5*time.Millisecond, nil)
	defer restore()
	stop := Start("thinking")
	time.Sleep(15 * time.Millisecond)
	stop()
	if buf.Len() != 0 {
		t.Fatalf("wrote %q", buf.String())
	}
}

func TestSpinnerElapsedAfter1s(t *testing.T) {
	var buf bytes.Buffer
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var now time.Time
	now = base
	restore := TestHook(&buf, true, 5*time.Millisecond, func() time.Time { return now })
	defer restore()
	stop := Start("working")
	time.Sleep(8 * time.Millisecond)
	now = base.Add(1500 * time.Millisecond)
	time.Sleep(12 * time.Millisecond)
	stop()
	if !strings.Contains(buf.String(), "1.5s") {
		t.Fatalf("missing elapsed: %q", buf.String())
	}
}

func TestStartAfterDelay(t *testing.T) {
	var buf bytes.Buffer
	restore := TestHook(&buf, true, 5*time.Millisecond, nil)
	defer restore()
	stop := StartAfter("loading memory", 30*time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	if strings.Contains(buf.String(), "loading memory") {
		stop()
		t.Fatal("showed before delay")
	}
	time.Sleep(40 * time.Millisecond)
	stop()
	if !strings.Contains(buf.String(), "loading memory") {
		t.Fatalf("missing after delay: %q", buf.String())
	}
}

func TestQuietStartsAfterSilenceAndRestarts(t *testing.T) {
	var buf bytes.Buffer
	restore := TestHook(&buf, true, 5*time.Millisecond, nil)
	defer restore()
	q := NewQuiet("running", 20*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	if !strings.Contains(buf.String(), "running") {
		q.Stop()
		t.Fatalf("missing first run: %q", buf.String())
	}
	_, _ = q.Write([]byte("out"))
	buf.Reset()
	time.Sleep(8 * time.Millisecond)
	if strings.Contains(buf.String(), "running") {
		q.Stop()
		t.Fatalf("still spinning after output: %q", buf.String())
	}
	time.Sleep(40 * time.Millisecond)
	q.Stop()
	if !strings.Contains(buf.String(), "running") {
		t.Fatalf("did not restart: %q", buf.String())
	}
}

func TestSpinnerCallSiteLabels(t *testing.T) {
	labels := []string{"understanding", "loading memory", "thinking", "planning", "working", "running", "exporting", "fetching"}
	for _, label := range labels {
		var buf bytes.Buffer
		restore := TestHook(&buf, true, 5*time.Millisecond, nil)
		stop := Start(label)
		time.Sleep(12 * time.Millisecond)
		stop()
		restore()
		if !strings.Contains(buf.String(), hideCursor) || !strings.Contains(buf.String(), label) {
			t.Fatalf("%s: %q", label, buf.String())
		}
	}
}
