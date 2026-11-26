package redact

import (
	"strings"
	"testing"
)

// TestMaskValuesMasksAuthSchemeRemainder is the P0-B leak repro: masking a
// two-token auth value ("Authorization: Bearer <key>") used to mask only the
// scheme word and leave the key after it.
func TestMaskValuesMasksAuthSchemeRemainder(t *testing.T) {
	key := "sk" + "-" + "p0b" + "-" + "5a1c7e93b2d4068f"
	got, n := MaskValues("Authorization: Bearer " + key + "\n")
	if n != 1 {
		t.Fatalf("masked %d lines", n)
	}
	if strings.Contains(got, key) {
		t.Fatalf("the auth scheme hid the key: %q", got)
	}
	if !strings.Contains(got, "Authorization:") || !strings.Contains(got, "masked") {
		t.Fatalf("the line was not masked: %q", got)
	}
}

// TestMaskValuesMasksSpacedValue: an env value may contain spaces, so the whole
// remainder of the assignment must be masked.
func TestMaskValuesMasksSpacedValue(t *testing.T) {
	got, n := MaskValues("PASSWORD=correct horse battery staple\n")
	if n != 1 {
		t.Fatalf("masked %d lines", n)
	}
	if strings.Contains(got, "horse") || strings.Contains(got, "staple") {
		t.Fatalf("a spaced value leaked: %q", got)
	}
}

// TestMaskValuesKeepsPlainLines leaves non-assignment lines untouched.
func TestMaskValuesKeepsPlainLines(t *testing.T) {
	in := "# a comment\nplain text\n\nKEY=\n"
	got, n := MaskValues(in)
	if n != 0 {
		t.Fatalf("masked %d lines", n)
	}
	if got != in {
		t.Fatalf("plain lines changed: %q", got)
	}
}
