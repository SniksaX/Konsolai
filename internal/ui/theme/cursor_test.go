package theme

import (
	"bytes"
	"strings"
	"testing"
)

func TestRestoreCursorPath(t *testing.T) {
	defer Force(true, true, "amber", Dark)()
	SetCursorColor(true)
	var buf bytes.Buffer
	ApplyCursor(&buf)
	if !strings.Contains(buf.String(), "]12;") {
		t.Fatalf("missing OSC 12: %q", buf.String())
	}
	if !strings.Contains(buf.String(), RoleRGB(Heading).Hex()[1:]) && !strings.Contains(buf.String(), "FFE0A3") {
		t.Fatalf("OSC 12 color: %q", buf.String())
	}
	ApplyCursor(&buf)
	RestoreCursor(&buf)
	if !strings.Contains(buf.String(), "]112") {
		t.Fatalf("missing OSC 112: %q", buf.String())
	}
	n := strings.Count(buf.String(), "]112")
	RestoreCursor(&buf)
	if strings.Count(buf.String(), "]112") != n {
		t.Fatal("second RestoreCursor should be a no-op")
	}
}

func TestRestoreCursorOnPanic(t *testing.T) {
	defer Force(true, true, "amber", Dark)()
	SetCursorColor(true)
	var buf bytes.Buffer
	func() {
		defer RestoreCursorAlways(&buf)
		ApplyCursor(&buf)
		defer func() { _ = recover() }()
		panic("cursor")
	}()
	if !strings.Contains(buf.String(), "]112") {
		t.Fatalf("panic path skipped OSC 112: %q", buf.String())
	}
}

func TestRestoreCursorAlwaysWrites(t *testing.T) {
	var buf bytes.Buffer
	RestoreCursorAlways(&buf)
	if buf.String() != OSC112() {
		t.Fatalf("got %q", buf.String())
	}
}

func TestOSC12UsesHeading(t *testing.T) {
	c := hexRGB(0xFFE0A3)
	s := OSC12(c)
	if !strings.Contains(s, "#FFE0A3") {
		t.Fatalf("%q", s)
	}
}
