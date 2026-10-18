package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestThemeAliases(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"amber", "amber"},
		{"AMBER", "amber"},
		{"glacier", "glacier"},
		{"cyan", "glacier"},
		{"ocean", "glacier"},
		{"nebula", "nebula"},
		{"violet", "nebula"},
		{"jade", "jade"},
		{"green", "jade"},
		{"forest", "jade"},
		{"rose", "rose"},
		{"red", "rose"},
		{"mono", "mono"},
	}
	for _, tc := range cases {
		got, ok := ResolveTheme(tc.in)
		if !ok || got != tc.want {
			t.Fatalf("ResolveTheme(%q) = %q, %v; want %q, true", tc.in, got, ok, tc.want)
		}
	}
	if _, ok := ResolveTheme("neon"); ok {
		t.Fatal("neon should be unknown")
	}
}

func TestSetThemeKeepsCommentsAndOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	src := `# keep this header
default_profile: home

defaults:
  ui:
    theme: amber   # chosen look
    appearance: auto   # appearance
    banner: true
# trailing note
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SetTheme(path, "ocean"); err != nil {
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
		"theme: glacier",
		"# chosen look",
		"appearance: auto",
		"# appearance",
		"banner: true",
		"# trailing note",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "theme: amber") {
		t.Fatalf("old theme left:\n%s", s)
	}
	themeAt := strings.Index(s, "theme:")
	appAt := strings.Index(s, "appearance:")
	banAt := strings.Index(s, "banner:")
	if themeAt < 0 || appAt < 0 || banAt < 0 || !(themeAt < appAt && appAt < banAt) {
		t.Fatalf("order lost:\n%s", s)
	}
}

func TestSetThemeRejectsUnknown(t *testing.T) {
	if err := SetTheme(filepath.Join(t.TempDir(), "c.yaml"), "neon"); err == nil {
		t.Fatal("expected error")
	}
}

func TestMigrateOldThemeAndPalette(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	src := `# keep this header
default_profile: home

defaults:
  ui:
    theme: dark   # appearance
    palette: violet   # chosen look
    banner: true

profiles:
  home: {}
# trailing note
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Notice == "" || !strings.Contains(f.Notice, "migrated") {
		t.Fatalf("notice %q", f.Notice)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, want := range []string{
		"# keep this header",
		"theme: nebula",
		"appearance: dark",
		"# appearance",
		"banner: true",
		"# trailing note",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "palette:") {
		t.Fatalf("palette left:\n%s", s)
	}
	if strings.Contains(s, "theme: dark") {
		t.Fatalf("old theme left:\n%s", s)
	}
	c, err := f.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if c.PaletteName() != "nebula" {
		t.Fatalf("theme %q", c.PaletteName())
	}
	if c.Appearance() != "dark" {
		t.Fatalf("appearance %q", c.Appearance())
	}

	f2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f2.Notice, "ui.theme") {
		t.Fatalf("second load migrated again: %q", f2.Notice)
	}
}

func TestMigrateOldThemeOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	src := `defaults:
  ui:
    theme: light
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.Notice, "migrated") {
		t.Fatalf("notice %q", f.Notice)
	}
	c, err := f.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if c.PaletteName() != "amber" {
		t.Fatalf("theme %q", c.PaletteName())
	}
	if c.Appearance() != "light" {
		t.Fatalf("appearance %q", c.Appearance())
	}
}

func TestMigrateOldPaletteOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	src := `defaults:
  ui:
    palette: ocean
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if c.PaletteName() != "glacier" {
		t.Fatalf("theme %q", c.PaletteName())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "theme: glacier") {
		t.Fatalf("file:\n%s", body)
	}
	if strings.Contains(string(body), "palette:") {
		t.Fatalf("palette left:\n%s", body)
	}
}
