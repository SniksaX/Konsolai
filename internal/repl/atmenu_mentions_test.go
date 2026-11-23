package repl

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAtMenuUsesMentionRows: a mention source replaces the file list, and an
// empty answer restores it.
func TestAtMenuUsesMentionRows(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newAtMenu(func() string { return dir })
	m.SetMentions(func(prefix string) []atEntry {
		if prefix != "" && prefix != "a" {
			return nil
		}
		return []atEntry{
			{rel: "all", meta: "everyone"},
			{rel: "alpha", meta: "the first"},
		}
	})
	m.Sync("ask @", len([]rune("ask @")))
	if !m.Open() || len(m.rows) != 2 {
		t.Fatalf("rows %v open %v", m.rows, m.Open())
	}
	if m.rows[0].rel != "all" || m.rows[1].rel != "alpha" {
		t.Fatalf("rows %v", m.rows)
	}
	for _, row := range m.rows {
		if row.rel == "notes.txt" {
			t.Fatalf("a file row leaked: %v", m.rows)
		}
	}
	// Accept inserts @value and a space, and keeps editing.
	res := m.Enter()
	if res.Line != "ask @all " {
		t.Fatalf("line %q", res.Line)
	}
	// A prefix the source declines falls back to the file list.
	m.Sync("ask @n", len([]rune("ask @n")))
	if !m.Open() || len(m.rows) == 0 || m.rows[0].rel != "notes.txt" {
		t.Fatalf("file rows after a decline: %v", m.rows)
	}
	// Esc closes the menu and leaves the typed text alone.
	esc := m.Esc()
	if esc.Line != "ask @n" || m.Open() {
		t.Fatalf("esc %+v open %v", esc, m.Open())
	}
}
