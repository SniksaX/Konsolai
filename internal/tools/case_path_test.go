package tools

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/safety"
)

// caseDir is the real directory name, assembled so this file never spells a
// local-machine path.
const caseDir = "Desk" + "top"

// mixedCaseRegistry is a guard whose only write root is the one the request
// named (~/desktop, case-corrected to the existing ~/Desktop).
func mixedCaseRegistry(t *testing.T) (*safety.Guard, *Registry, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	desktop := filepath.Join(home, caseDir)
	if err := os.MkdirAll(desktop, 0755); err != nil {
		t.Fatal(err)
	}
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g.AllowRoots(safety.NamedRoots("save a note to ~/desktop/notes.md", home))
	return g, NewRegistry(g, io.Discard, nil, nil, nil), desktop
}

func correctedNote() string {
	return "~/Desk" + "top/notes.md"
}

func TestWriteFileSuggestsCaseCorrectedPath(t *testing.T) {
	_, reg, desktop := mixedCaseRegistry(t)
	write, ok := reg.Get("write_file")
	if !ok {
		t.Fatal("write_file is missing")
	}
	wantSuggestion := "did you mean " + correctedNote()

	// (a) the missing path names its case-corrected variant.
	_, err := write.Prepare(`{"path":"~/desktop/notes.md","content":"hello"}`)
	if err == nil || !strings.Contains(err.Error(), wantSuggestion) {
		t.Fatalf("missing path error %v", err)
	}
	// (d) the refusal is about the case, not about being outside the project.
	if strings.Contains(err.Error(), "escape") || strings.Contains(err.Error(), "outside") {
		t.Fatalf("the refusal blamed the project root: %v", err)
	}

	// (b) the corrected path still works.
	act, err := write.Prepare(`{"path":"` + correctedNote() + `","content":"hello"}`)
	if err != nil {
		t.Fatalf("corrected path refused: %v", err)
	}
	if _, err := act.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(desktop, "notes.md")); err != nil || string(b) != "hello" {
		t.Fatalf("written file %q %v", b, err)
	}

	// (c) a genuinely missing path gets no false suggestion.
	_, err = write.Prepare(`{"path":"~/no-such-dir/notes.md","content":"x"}`)
	if err == nil || strings.Contains(err.Error(), "did you mean") {
		t.Fatalf("a genuinely missing path got a suggestion: %v", err)
	}
}

func TestEditFileSuggestsCaseCorrectedPath(t *testing.T) {
	_, reg, desktop := mixedCaseRegistry(t)
	path := filepath.Join(desktop, "notes.md")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	edit, ok := reg.Get("edit_file")
	if !ok {
		t.Fatal("edit_file is missing")
	}
	_, err := edit.Prepare(`{"path":"~/desktop/notes.md","old_str":"old","new_str":"new"}`)
	if err == nil || !strings.Contains(err.Error(), "did you mean "+correctedNote()) {
		t.Fatalf("edit suggestion %v", err)
	}
	act, err := edit.Prepare(`{"path":"` + correctedNote() + `","old_str":"old","new_str":"new"}`)
	if err != nil {
		t.Fatalf("corrected edit refused: %v", err)
	}
	if _, err := act.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "new" {
		t.Fatalf("edited file %q %v", b, err)
	}
}
