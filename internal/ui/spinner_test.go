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

func TestQuietClearedBeforeForwardedOutput(t *testing.T) {
	var buf bytes.Buffer
	restore := TestHook(&buf, true, time.Millisecond, nil)
	defer restore()
	q := NewQuiet("running", 5*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	if _, err := q.Tee(&buf).Write([]byte("hello\nworld\n")); err != nil {
		t.Fatal(err)
	}
	q.Stop()
	for _, line := range committedLines(buf.String()) {
		for _, f := range braille {
			if strings.HasPrefix(strings.TrimLeft(line, " "), f) {
				t.Fatalf("output line starts with spinner %q in %q (raw %q)", f, line, buf.String())
			}
		}
	}
	if !strings.Contains(buf.String(), "hello") {
		t.Fatalf("missing output: %q", buf.String())
	}
}

func committedLines(s string) []string {
	var cur []byte
	var lines []string
	for i := 0; i < len(s); i++ {
		if s[i] == '\033' {
			j := i + 1
			for j < len(s) && (s[j] < 'A' || s[j] > 'Z') && (s[j] < 'a' || s[j] > 'z') {
				j++
			}
			if j < len(s) && s[j] == 'K' {
				cur = cur[:0]
			}
			if j < len(s) {
				i = j
			}
			continue
		}
		if s[i] == '\r' {
			cur = cur[:0]
			continue
		}
		if s[i] == '\n' {
			lines = append(lines, string(cur))
			cur = cur[:0]
			continue
		}
		cur = append(cur, s[i])
	}
	if len(cur) > 0 {
		lines = append(lines, string(cur))
	}
	return lines
}

func TestLiveSetLabel(t *testing.T) {
	var buf bytes.Buffer
	restore := TestHook(&buf, true, 5*time.Millisecond, nil)
	defer restore()
	live := StartLive("thinking")
	time.Sleep(12 * time.Millisecond)
	live.SetLabel("writing · 300 tok")
	time.Sleep(12 * time.Millisecond)
	live.Stop()
	got := buf.String()
	if !strings.Contains(got, "thinking") || !strings.Contains(got, "writing · 300 tok") {
		t.Fatalf("%q", got)
	}
}

func TestHoldPromptBlocksSpinner(t *testing.T) {
	var buf bytes.Buffer
	restore := TestHook(&buf, true, 5*time.Millisecond, nil)
	defer restore()
	HoldPrompt(true)
	defer HoldPrompt(false)
	stop := Start("thinking")
	time.Sleep(20 * time.Millisecond)
	stop()
	ResetDisplay()
	if buf.Len() != 0 {
		t.Fatalf("drew while prompt held: %q", buf.String())
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
