package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const secretsFile = "secrets"

// SecretPath is ~/.config/konsolai/secrets. It is never inside the repo and
// never part of config.yaml.
func SecretPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: home directory: %w", err)
	}
	return filepath.Join(home, ".config", "konsolai", secretsFile), nil
}

// ReadSecret returns the stored key for a model name, if any.
func ReadSecret(model string) (string, bool) {
	path, err := SecretPath()
	if err != nil {
		return "", false
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(name) == model {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

// WriteSecret stores or replaces one model's key in the secrets file.
// The directory is 0700 and the file 0600.
func WriteSecret(model, key string) error {
	model = strings.TrimSpace(model)
	key = strings.TrimSpace(key)
	if model == "" || key == "" {
		return errors.New("config: a model name and a key are required")
	}
	if strings.ContainsAny(model, "=\r\n") || strings.ContainsAny(key, "\r\n") {
		return errors.New("config: the model name and key must be single-line values")
	}
	path, err := SecretPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: create secrets directory: %w", err)
	}
	_ = os.Chmod(dir, 0o700)
	lines := map[string]string{}
	if body, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			name, value, ok := strings.Cut(line, "=")
			if ok {
				lines[strings.TrimSpace(name)] = strings.TrimSpace(value)
			}
		}
	}
	lines[model] = key
	names := make([]string, 0, len(lines))
	for name := range lines {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# Konsolai API keys. Mode 0600. Never commit this file.\n")
	for _, name := range names {
		fmt.Fprintf(&b, "%s=%s\n", name, lines[name])
	}
	tmp, err := os.CreateTemp(dir, ".secrets-*")
	if err != nil {
		return fmt.Errorf("config: write secrets: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: secrets mode: %w", err)
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: secrets write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: secrets close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("config: secrets rename: %w", err)
	}
	return os.Chmod(path, 0o600)
}

// ModelAPIKey resolves a model's key at call time: the named environment
// variable first, then the secrets file. An empty key means no auth.
func (c Config) ModelAPIKey(model string) (string, error) {
	m, ok := c.Models[model]
	if !ok {
		return "", fmt.Errorf("config: model %q is not configured", model)
	}
	env := m.APIKeyVariable()
	if env != "" {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v, nil
		}
	}
	if v, ok := ReadSecret(model); ok {
		return v, nil
	}
	if env != "" {
		return "", fmt.Errorf("config: model %q: %s is not set and no key is stored; run /set key %s or export %s", model, env, model, env)
	}
	return "", nil
}

// APIKeyResolver is the call-time hook an llm.Client keeps.
func (c Config) APIKeyResolver(model string) func() (string, error) {
	return func() (string, error) { return c.ModelAPIKey(model) }
}
