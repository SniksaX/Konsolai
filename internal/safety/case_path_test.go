package safety

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNamedRootCorrectsCase covers a request that names a case-different path
// (~/desktop) while the real directory is ~/Desktop. The named root must be
// the case-corrected path so a write to the real directory is allowed, and a
// genuinely new directory must keep its given spelling.
func TestNamedRootCorrectsCase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	desktop := filepath.Join(home, "Desktop")
	if err := os.MkdirAll(desktop, 0755); err != nil {
		t.Fatal(err)
	}

	extras := NamedRoots("save a note to ~/desktop/notes.md", home)
	want := filepath.Join(desktop, "notes.md")
	if len(extras) != 1 || extras[0] != want {
		t.Fatalf("named roots %v, want [%s]", extras, want)
	}

	g, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g.AllowRoots(extras)
	// (b) the exact path still works, and (d) the corrected path is allowed.
	if _, err := g.ResolvePath("~/Desktop/notes.md"); err != nil {
		t.Fatalf("the corrected path was refused: %v", err)
	}
	// The case-different spelling is still not silently accepted.
	if _, err := g.ResolvePath("~/desktop/notes.md"); err == nil {
		t.Fatal("the wrong-case path resolved")
	}

	// (c) a genuinely new path keeps its case and gains no false correction.
	fresh := NamedRoots("make a directory ~/brand-new/dir", home)
	if len(fresh) != 1 || fresh[0] != filepath.Join(home, "brand-new", "dir") {
		t.Fatalf("new path %v", fresh)
	}
}

func TestSuggestPathOnlyForAnUnambiguousCaseVariant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, dir := range []string{"Desktop/Sub", "notes", "Case", "CASE"} {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(dir)), 0755); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"~/desktop/notes.md", "~/Desktop/notes.md", true},
		{"~/desktop/Sub/file.txt", "~/Desktop/Sub/file.txt", true},
		{filepath.Join(home, "desktop", "notes.md"), filepath.Join(home, "Desktop", "notes.md"), true},
		{"~/Desktop/notes.md", "", false}, // the directory is right, the file is new
		{"~/notes", "", false},            // the path exists
		{"~/missing/notes.md", "", false}, // no case variant anywhere
		{"~/case/x", "", false},           // two variants are ambiguous
		{"relative/desktop/notes.md", "", false},
	}
	for _, tc := range cases {
		got, ok := SuggestPath(tc.raw, "")
		if ok != tc.ok || got != tc.want {
			t.Errorf("SuggestPath(%q) = %q, %v; want %q, %v", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}
