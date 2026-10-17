package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetPaletteKeepsCommentsAndOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	src := `# keep this header
default_profile: home

defaults:
  ui:
    theme: auto   # appearance
    palette: amber   # chosen look
    banner: true
# trailing note
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SetPalette(path, "ocean"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, want := range []string{
		"# keep this header",
		"default_profile: home",
		"theme: auto",
		"# appearance",
		"palette: ocean",
		"# chosen look",
		"banner: true",
		"# trailing note",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "palette: amber") {
		t.Fatalf("old palette left:\n%s", s)
	}
	themeAt := strings.Index(s, "theme:")
	palAt := strings.Index(s, "palette:")
	banAt := strings.Index(s, "banner:")
	if themeAt < 0 || palAt < 0 || banAt < 0 || !(themeAt < palAt && palAt < banAt) {
		t.Fatalf("order lost:\n%s", s)
	}
}

func TestSetPaletteRejectsUnknown(t *testing.T) {
	if err := SetPalette(filepath.Join(t.TempDir(), "c.yaml"), "neon"); err == nil {
		t.Fatal("expected error")
	}
}
