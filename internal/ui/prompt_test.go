package ui

import (
	"bufio"
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
	p = &Prompter{tty: r, in: bufio.NewReader(r), out: out}
	return p, w, out
}

func TestConfirmPersistentReaderDrainsLeftover(t *testing.T) {
	p, w, out := testPrompter(t)
	done := make(chan Decision, 1)
	errc := make(chan error, 1)
	go func() {
		d, _, err := p.Confirm("do it", "")
		errc <- err
		done <- d
	}()
	waitPrompt(t, out, "do it")
	if _, err := w.Write([]byte("y\n\n")); err != nil {
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
	if strings.Count(out.String(), "again") > 1 && !strings.Contains(out.String(), "answer y, n, a or q") {
		t.Fatalf("re-prompted from leftover: %q", out.String())
	}
	if _, err := w.Write([]byte("n\nnope\n")); err != nil {
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
	if _, err := w.Write([]byte("x\ny\n")); err != nil {
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
