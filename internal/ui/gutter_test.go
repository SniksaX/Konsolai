package ui

import (
	"strings"
	"testing"
)

func TestNumbered(t *testing.T) {
	got := Numbered("echo a\necho b\necho c")
	for _, want := range []string{"1 ", "2 ", "3 ", "echo a", "echo b", "echo c"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("want trailing newline %q", got)
	}
}

func TestNumberedKeepsPlanNumbers(t *testing.T) {
	got := Numbered("1. mkdir a\n2. touch b")
	if strings.Contains(got, "1 1.") || strings.Contains(got, "2 2.") {
		t.Fatalf("duplicated numbers: %q", got)
	}
	if !strings.Contains(got, "1. mkdir a") || !strings.Contains(got, "2. touch b") {
		t.Fatalf("missing steps: %q", got)
	}
}

func TestIndentSkipsEmptyLines(t *testing.T) {
	got := Indent("a\n\nb\n", 2)
	if got != "  a\n\n  b\n" {
		t.Fatalf("%q", got)
	}
}

func TestGutterIndentsBar(t *testing.T) {
	var buf strings.Builder
	g := NewGutter(&buf)
	if _, err := g.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "    │ ") {
		t.Fatalf("%q", buf.String())
	}
}

func TestIsDiffPreview(t *testing.T) {
	if !isDiffPreview("--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new") {
		t.Fatal("unified diff")
	}
	if isDiffPreview("echo a\necho b") {
		t.Fatal("script is not a diff")
	}
}

func TestColorDiffHeadersAreNotChanges(t *testing.T) {
	restore := ForceTheme(Palette{Enabled: true, Truecolor: true, Scheme: "amber"})
	defer restore()
	p := Current()
	got := strings.Split(ColorDiff("--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-old\n+new"), "\n")
	want := []string{p.Heading("--- a/x.go"), p.Heading("+++ b/x.go"), p.Accent("@@ -1 +1 @@"), p.Error("-old"), p.Success("+new")}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d: %q want %q", i, got[i], want[i])
		}
	}
}
