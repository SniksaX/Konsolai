package diff

import (
	"strings"
	"testing"
)

func TestUnifiedInsert(t *testing.T) {
	got := Unified("a.txt", "a\nb\n", "a\nx\nb\n", 3)
	if !strings.Contains(got, "--- a/a.txt") || !strings.Contains(got, "+++ b/a.txt") {
		t.Fatalf("headers: %q", got)
	}
	if !strings.Contains(got, "+x") {
		t.Fatalf("missing insert: %q", got)
	}
	if add, rem := Stat("a\nb\n", "a\nx\nb\n"); add != 1 || rem != 0 {
		t.Fatalf("stat %d %d", add, rem)
	}
}

func TestUnifiedDelete(t *testing.T) {
	got := Unified("a.txt", "a\nx\nb\n", "a\nb\n", 3)
	if !strings.Contains(got, "-x") {
		t.Fatalf("%q", got)
	}
	if add, rem := Stat("a\nx\nb\n", "a\nb\n"); add != 0 || rem != 1 {
		t.Fatalf("stat %d %d", add, rem)
	}
}

func TestUnifiedModify(t *testing.T) {
	got := Unified("a.txt", "hello\n", "world\n", 3)
	if !strings.Contains(got, "-hello") || !strings.Contains(got, "+world") {
		t.Fatalf("%q", got)
	}
}

func TestUnifiedIdentical(t *testing.T) {
	if Unified("a.txt", "x\n", "x\n", 3) != "" {
		t.Fatal("want empty")
	}
	if a, r := Stat("x\n", "x\n"); a != 0 || r != 0 {
		t.Fatal("stat")
	}
}

func TestUnifiedNoTrailingNewline(t *testing.T) {
	got := Unified("a.txt", "hello", "hello\nworld", 3)
	if !strings.Contains(got, "\\ No newline at end of file") {
		t.Fatalf("missing no-newline marker: %q", got)
	}
	if !strings.Contains(got, "+world") {
		t.Fatalf("%q", got)
	}
}

func TestUnifiedEmptyToContent(t *testing.T) {
	got := Unified("a.txt", "", "a\nb\n", 3)
	if !strings.Contains(got, "@@ -0,0 +1,2 @@") {
		t.Fatalf("hunk header: %q", got)
	}
	if !strings.Contains(got, "+a") || !strings.Contains(got, "+b") {
		t.Fatalf("%q", got)
	}
}

func TestUnifiedContentToEmpty(t *testing.T) {
	got := Unified("a.txt", "a\nb\n", "", 3)
	if !strings.Contains(got, "@@ -1,2 +0,0 @@") {
		t.Fatalf("hunk header: %q", got)
	}
}

func TestUnifiedMultipleHunks(t *testing.T) {
	old := "a\nb\nc\nd\ne\nf\ng\nh\ni\n"
	new := "A\nb\nc\nd\ne\nf\ng\nh\nI\n"
	got := Unified("a.txt", old, new, 1)
	if strings.Count(got, "@@") < 2 {
		t.Fatalf("want two hunks: %q", got)
	}
}

func TestUnifiedTooLarge(t *testing.T) {
	old := strings.Repeat("x\n", 5001)
	got := Unified("a.txt", old, "y\n", 3)
	if !strings.Contains(got, "diff too large: 5001 -> 1 lines") {
		t.Fatalf("%q", got)
	}
}
