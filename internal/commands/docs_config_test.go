package commands

import (
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

// TestDocsFriendlyTable keeps docs/configuration.md aligned with Default().
// Columns are name, values, and keys, in the order of config.FriendlyTable().
func TestDocsFriendlyTable(t *testing.T) {
	text := readDoc(t, "docs/configuration.md")
	if !strings.Contains(text, "| name | values | keys |") {
		t.Fatal("configuration doc missing friendly table")
	}
	for _, line := range strings.Split(strings.TrimSpace(config.FriendlyTable()), "\n") {
		fields := strings.Split(line, "|")
		if len(fields) < 3 {
			continue
		}
		name := strings.TrimSpace(fields[1])
		if name == "" || name == "name" || name == "---" {
			continue
		}
		if !strings.Contains(text, "| "+name+" |") {
			t.Errorf("configuration doc missing friendly name %s", name)
		}
	}
}

// TestDocsEnvOverrides keeps the environment tables in docs/configuration.md.
func TestDocsEnvOverrides(t *testing.T) {
	text := readDoc(t, "docs/configuration.md")
	if strings.Contains(text, ".env.example") || strings.Contains(text, "config.default.yaml") {
		t.Fatal("configuration doc names a removed config file")
	}
	section := docSection(text, "## Environment overrides")
	if section == "" {
		t.Fatal("configuration doc missing Environment overrides")
	}
	note := "Prefer `config.yaml`. Environment variables are for containers and CI."
	if !strings.Contains(section, note) {
		t.Fatal("configuration doc missing the environment-override note")
	}
	if !strings.Contains(section, config.EnvOverrideTable()) {
		t.Fatalf("config env table drifted\nwant:\n%s", config.EnvOverrideTable())
	}
	if !strings.Contains(section, config.ComposeVarTable()) {
		t.Fatalf("compose var table drifted\nwant:\n%s", config.ComposeVarTable())
	}
	for _, k := range config.Keys() {
		if k.Env == "" {
			continue
		}
		if !strings.Contains(section, k.Env) || !strings.Contains(section, k.Path) {
			t.Errorf("section missing %s (%s)", k.Env, k.Path)
		}
	}
}

// docSection returns the text between a heading and the next heading of the
// same or a higher level.
func docSection(text, heading string) string {
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if line == heading {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}
