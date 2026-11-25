package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretsFileModesAndLookup(t *testing.T) {
	keyAlpha := "sk" + "-alpha-secret"
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KONSOLAI_TEST_KEY", "")

	if err := WriteSecret("alpha", keyAlpha); err != nil {
		t.Fatal(err)
	}
	path, err := SecretPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", "konsolai", "secrets")
	if path != want {
		t.Fatalf("path %q want %q", path, want)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v err %v", fi.Mode().Perm(), err)
	}
	if fi, err := os.Stat(filepath.Dir(path)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v err %v", fi.Mode().Perm(), err)
	}
	if key, ok := ReadSecret("alpha"); !ok || key != keyAlpha {
		t.Fatalf("read %q %v", key, ok)
	}

	c := Config{Models: map[string]Model{"alpha": {APIKeyEnv: "KONSOLAI_TEST_KEY"}}}
	if key, err := c.ModelAPIKey("alpha"); err != nil || key != keyAlpha {
		t.Fatalf("fallback key %q err %v", key, err)
	}

	t.Setenv("KONSOLAI_TEST_KEY", "sk"+"-from-env")
	if key, err := c.ModelAPIKey("alpha"); err != nil || key != "sk"+"-from-env" {
		t.Fatalf("env key %q err %v", key, err)
	}

	// The env var is read at call time, not captured at construction.
	resolver := c.APIKeyResolver("alpha")
	t.Setenv("KONSOLAI_TEST_KEY", "sk"+"-rotated")
	if key, err := resolver(); err != nil || key != "sk"+"-rotated" {
		t.Fatalf("rotated key %q err %v", key, err)
	}
}

func TestMissingKeyNamesTheEnvironmentVariable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KONSOLAI_TEST_KEY", "")
	c := Config{Models: map[string]Model{"alpha": {APIKeyEnv: "KONSOLAI_TEST_KEY"}}}
	key, err := c.ModelAPIKey("alpha")
	if err == nil || key != "" {
		t.Fatalf("want missing-key error, got %q %v", key, err)
	}
	if !strings.Contains(err.Error(), "KONSOLAI_TEST_KEY") || !strings.Contains(err.Error(), "/set key alpha") {
		t.Fatalf("error does not name the env var or the fix: %v", err)
	}
	if strings.Contains(err.Error(), "sk-") {
		t.Fatalf("error leaks a key: %v", err)
	}
}

func TestSecretValueNeverRendered(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KONSOLAI_TEST_KEY", "sk"+"-never-render-me")
	c := Builtin()
	m := c.Models["default"]
	m.APIKeyEnv = "KONSOLAI_TEST_KEY"
	c.Models["default"] = m
	out := renderShow(c)
	if strings.Contains(out, "sk"+"-never-render-me") {
		t.Fatal("config show rendered the key")
	}
	if !strings.Contains(out, "KONSOLAI_TEST_KEY") {
		t.Fatal("config show should name the environment variable")
	}
}
