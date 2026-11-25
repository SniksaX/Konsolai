package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeepSeekPresetUsesAutoToolChoice(t *testing.T) {
	p, ok := Provider("deepseek")
	if !ok || p.ToolChoiceMode != "auto" {
		t.Fatalf("deepseek preset %+v", p)
	}
	var m Model
	ApplyProvider(&m, p)
	if m.ChatToolChoice() != "auto" {
		t.Fatalf("applied model %+v", m)
	}
}

func TestChatToolChoiceFor(t *testing.T) {
	c := Builtin()
	c.Models["local"] = Model{Endpoint: "http://127.0.0.1:8000/v1"}
	c.Models["auto"] = Model{Endpoint: "https://api.example/v1", ToolChoiceMode: "auto"}
	c.Models["required"] = Model{Endpoint: "https://api.example/v1", ToolChoiceMode: "required"}
	if got := c.ChatToolChoiceFor("local"); got != "named" {
		t.Fatalf("local default %q", got)
	}
	if got := c.ChatToolChoiceFor("auto"); got != "auto" {
		t.Fatalf("model auto %q", got)
	}
	if got := c.ChatToolChoiceFor("required"); got != "required" {
		t.Fatalf("model required %q", got)
	}
	c.LLM.ToolChoiceMode = "required"
	if got := c.ChatToolChoiceFor("local"); got != "required" {
		t.Fatalf("global fallback %q", got)
	}
	if got := c.ChatToolChoiceFor("auto"); got != "auto" {
		t.Fatalf("model must win over the global default: %q", got)
	}
}

func TestModelToolChoiceAndThinkingParsing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := "defaults:\n  models:\n    api:\n      endpoint: https://api.deepseek.com/v1\n" +
		"      model: deepseek-flash\n      tool_choice_mode: auto\n      thinking_mode: disabled\n" +
		"  roles:\n    main: api\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
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
	if c.ChatToolChoiceFor("api") != "auto" || c.Models["api"].ChatThinking() != "disabled" {
		t.Fatalf("parsed model %+v", c.Models["api"])
	}
}

func TestInvalidToolChoiceAndThinkingAreRejected(t *testing.T) {
	for _, tc := range []struct{ field, value string }{
		{"tool_choice_mode", "sometimes"},
		{"thinking_mode", "maybe"},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		body := "defaults:\n  models:\n    api:\n      endpoint: https://api.deepseek.com/v1\n" +
			"      model: deepseek-flash\n      " + tc.field + ": " + tc.value + "\n  roles:\n    main: api\n"
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		f, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.Resolve("")
		if err == nil || !strings.Contains(err.Error(), tc.field) {
			t.Fatalf("%s=%s must be rejected: %v", tc.field, tc.value, err)
		}
	}
}

func configWithModel(t *testing.T, name, field, value string) Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := "defaults:\n  models:\n    " + name + ":\n      endpoint: https://api.deepseek.com/v1\n" +
		"      model: deepseek-flash\n      " + field + ": " + value + "\n  roles:\n    main: " + name + "\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
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
	return c
}

func TestInitYAMLDocumentsTheNewModelFields(t *testing.T) {
	body := InitYAML()
	for _, want := range []string{"tool_choice_mode:", "thinking_mode:"} {
		if !strings.Contains(body, want) {
			t.Fatalf("generated config misses %q", want)
		}
	}
}
