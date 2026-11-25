package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReasoningEffortResolvesAndOmitsDefault(t *testing.T) {
	c := Builtin()
	c.Models["luna"] = Model{Endpoint: "https://api.openai.com/v1", ReasoningEffort: "high"}
	c.Models["plain"] = Model{Endpoint: "https://api.openai.com/v1"}
	c.Roles.Main = "plain"
	if got := c.ChatReasoningEffortFor("luna"); got != "high" {
		t.Fatalf("model effort %q", got)
	}
	if got := c.ChatReasoningEffortFor("plain"); got != "" {
		t.Fatalf("default must omit the field: %q", got)
	}
	if got := c.ReasoningHeadroomFor("luna"); got <= 0 {
		t.Fatalf("a reasoning model needs headroom, got %d", got)
	}
	c.Models["none"] = Model{Endpoint: "https://api.openai.com/v1", ReasoningEffort: "none"}
	if got := c.ReasoningHeadroomFor("none"); got != 0 {
		t.Fatalf("reasoning_effort none needs no headroom, got %d", got)
	}
	c.LLM.ReasoningEffort = "low"
	if got := c.ChatReasoningEffortFor("plain"); got != "low" {
		t.Fatalf("global fallback %q", got)
	}
}

func TestReasoningEffortParsingAndValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := "defaults:\n  models:\n    luna:\n      endpoint: https://api.openai.com/v1\n      reasoning_effort: minimal\n  roles:\n    main: luna\n"
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
	if c.ChatReasoningEffortFor("luna") != "minimal" {
		t.Fatalf("parsed %+v", c.Models["luna"])
	}
	bad := strings.Replace(body, "minimal", "maximum", 1)
	if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}
	f, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Resolve(""); err == nil || !strings.Contains(err.Error(), "reasoning_effort") {
		t.Fatalf("invalid value must be rejected: %v", err)
	}
	if !strings.Contains(InitYAML(), "reasoning_effort:") {
		t.Fatal("the generated config misses reasoning_effort")
	}
	p, ok := Provider("openai")
	if !ok || p.MaxTokensField != "max_completion_tokens" {
		t.Fatalf("openai preset %+v", p)
	}
}
