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

func TestIsDiffPreview(t *testing.T) {
	if !isDiffPreview("--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new") {
		t.Fatal("unified diff")
	}
	if isDiffPreview("echo a\necho b") {
		t.Fatal("script is not a diff")
	}
}
