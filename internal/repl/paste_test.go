package repl

import (
	"strings"
	"testing"
)

func TestShouldChipThresholds(t *testing.T) {
	ten := strings.Repeat("a\n", 9) + "a"
	if countLines(ten) != 10 || ShouldChip(ten) {
		t.Fatalf("10 lines should not chip lines=%d chip=%v", countLines(ten), ShouldChip(ten))
	}
	eleven := strings.Repeat("a\n", 10) + "a"
	if countLines(eleven) != 11 || !ShouldChip(eleven) {
		t.Fatalf("11 lines should chip lines=%d chip=%v", countLines(eleven), ShouldChip(eleven))
	}
	if ShouldChip(strings.Repeat("x", 999)) {
		t.Fatal("999 chars should not chip")
	}
	if !ShouldChip(strings.Repeat("x", 1000)) {
		t.Fatal("1000 chars should chip")
	}
}

func TestPasteExpandMultipleChips(t *testing.T) {
	p := NewPasteStore()
	c1, ok := p.MaybeChip(strings.Repeat("one\n", 12))
	if !ok || c1 != "[pasted #1 · 12 lines]" {
		t.Fatalf("c1 %q ok=%v", c1, ok)
	}
	c2, ok := p.MaybeChip(strings.Repeat("y", 1000))
	if !ok || !strings.HasPrefix(c2, "[pasted #2 ·") {
		t.Fatalf("c2 %q ok=%v", c2, ok)
	}
	line := "see " + c1 + " and " + c2
	got := p.Expand(line)
	if !strings.Contains(got, "one") || !strings.Contains(got, strings.Repeat("y", 8)) {
		t.Fatalf("expand %q", got)
	}
	if strings.Contains(got, "[pasted #") {
		t.Fatalf("chips left in %q", got)
	}
}

func TestEditorChipUndoThenSubmit(t *testing.T) {
	ed := NewInputEditor(nil)
	ed.Insert("keep ")
	raw := strings.Repeat("z\n", 11)
	ed.Insert(raw)
	if !strings.Contains(ed.Line(), "[pasted #1") {
		t.Fatalf("chip line %q", ed.Line())
	}
	visible := ed.Line()
	if ed.Submit() == visible {
		t.Fatal("submit should expand")
	}
	if !strings.Contains(ed.Submit(), "z") {
		t.Fatalf("submit %q", ed.Submit())
	}
	ed.Undo()
	if !strings.Contains(ed.Line(), "z\n") || strings.Contains(ed.Line(), "[pasted #") {
		t.Fatalf("ctrl-z expand %q", ed.Line())
	}
	ed.Undo()
	if ed.Line() != "keep " {
		t.Fatalf("second undo %q", ed.Line())
	}
}
