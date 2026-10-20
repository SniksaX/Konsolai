package ui

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func testPrompter(t *testing.T) (p *Prompter, write *os.File, out *bytes.Buffer) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	out = &bytes.Buffer{}
	p = &Prompter{
		out: out,
		read: func() (byte, error) {
			var b [1]byte
			_, err := r.Read(b[:])
			return b[0], err
		},
	}
	return p, w, out
}

func TestConfirmSummaryUsesHeading(t *testing.T) {
	restore := ForceTheme(Palette{Enabled: true, Truecolor: true, Scheme: "glacier"})
	defer restore()
	p, w, out := testPrompter(t)
	done := make(chan Decision, 1)
	go func() {
		d, _, err := p.Confirm("plan: add a file", "")
		if err != nil {
			t.Error(err)
		}
		done <- d
	}()
	waitPrompt(t, out, "plan: add a file")
	if _, err := w.Write([]byte("n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	s := out.String()
	mark := "plan: add a file"
	i := strings.Index(s, mark)
	if i < 0 || strings.Contains(s[:i], "\x1b[1") || strings.Contains(s, ";39m") {
		t.Fatalf("summary %q", s)
	}
}

func TestConfirmKeepsTypedAhead(t *testing.T) {
	p, w, _ := testPrompter(t)
	if _, err := w.Write([]byte("y")); err != nil {
		t.Fatal(err)
	}
	d, _, err := p.Confirm("do it", "")
	if err != nil {
		t.Fatal(err)
	}
	if d != Approve {
		t.Fatalf("%v", d)
	}
}

func TestConfirmSingleKeyNoEnter(t *testing.T) {
	p, w, out := testPrompter(t)
	done := make(chan Decision, 1)
	errc := make(chan error, 1)
	go func() {
		d, _, err := p.Confirm("do it", "")
		errc <- err
		done <- d
	}()
	waitPrompt(t, out, "do it")
	if _, err := w.Write([]byte("y")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first confirm timeout")
	}
	if <-done != Approve {
		t.Fatal("want Approve")
	}

	out.Reset()
	go func() {
		d, _, err := p.Confirm("again", "")
		errc <- err
		done <- d
	}()
	waitPrompt(t, out, "again")
	if _, err := w.Write([]byte("n")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second confirm timeout")
	}
	if <-done != Reject {
		t.Fatal("want Reject")
	}
}

func TestConfirmInvalidWarns(t *testing.T) {
	p, w, out := testPrompter(t)
	done := make(chan Decision, 1)
	go func() {
		d, _, err := p.Confirm("sum", "")
		if err != nil {
			t.Error(err)
		}
		done <- d
	}()
	waitPrompt(t, out, "sum")
	if _, err := w.Write([]byte("xy")); err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-done:
		if d != Approve {
			t.Fatalf("%v", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	if !strings.Contains(out.String(), "answer y, n, a or q") {
		t.Fatalf("missing warn: %q", out.String())
	}
}

func TestConfirmScriptGutter(t *testing.T) {
	p, w, out := testPrompter(t)
	done := make(chan Decision, 1)
	go func() {
		d, _, err := p.Confirm("run: echo a (+1 lines)", "echo a\necho b")
		if err != nil {
			t.Error(err)
		}
		done <- d
	}()
	waitPrompt(t, out, "echo a")
	if _, err := w.Write([]byte("y")); err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-done:
		if d != Approve {
			t.Fatalf("%v", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	s := out.String()
	if !strings.Contains(s, "1 ") || !strings.Contains(s, "2 ") || !strings.Contains(s, "echo b") {
		t.Fatalf("gutter %q", s)
	}
	if !strings.Contains(s, "    1 ") {
		t.Fatalf("preview not indented: %q", s)
	}
}

func TestConfirmPlanNoDoubleNumbers(t *testing.T) {
	p, w, out := testPrompter(t)
	done := make(chan Decision, 1)
	go func() {
		d, _, err := p.Confirm("plan", "1. mkdir a\n2. touch b")
		if err != nil {
			t.Error(err)
		}
		done <- d
	}()
	waitPrompt(t, out, "1. mkdir a")
	if _, err := w.Write([]byte("y")); err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-done:
		if d != Approve {
			t.Fatalf("%v", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	s := out.String()
	if strings.Contains(s, "1 1.") {
		t.Fatalf("duplicated numbers: %q", s)
	}
}

func TestOpenPrompterSkipsDevTTY(t *testing.T) {
	p, err := OpenPrompter(true)
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := p.Confirm("x", "")
	if err != nil || d != Approve {
		t.Fatalf("%v %v", d, err)
	}
}

func waitPrompt(t *testing.T, out *bytes.Buffer, needle string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), needle) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("prompt %q not seen in %q", needle, out.String())
}
