package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

func TestSetKeyRefusesPlainArguments(t *testing.T) {
	env, out, errb := testEnv(t)
	if code := runSet(context.Background(), env, []string{"key", "default", "sk" + "-plain-secret"}); code != 2 {
		t.Fatalf("code %d", code)
	}
	all := out.String() + errb.String()
	if strings.Contains(all, "sk"+"-plain-secret") {
		t.Fatal("the key was echoed")
	}
	if !strings.Contains(errb.String(), "usage: /set key") {
		t.Fatalf("stderr %q", errb.String())
	}
}

func TestSetKeyNeedsATerminal(t *testing.T) {
	env, out, errb := testEnv(t)
	if code := runSet(context.Background(), env, []string{"key", "default"}); code != 2 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(errb.String(), "terminal") {
		t.Fatalf("stderr %q", errb.String())
	}
	if strings.Contains(out.String(), "sk-") {
		t.Fatal("unexpected key echo")
	}
}

func TestSetModelAddProviderPreset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("defaults:\n  models:\n    default:\n      endpoint: http://127.0.0.1:9/v1\n  roles:\n    main: default\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, out, errb := testEnv(t)
	env.ConfigPath = path
	env.Profile = config.Builtin()

	if code := runSet(context.Background(), env, []string{"model", "add", "pub", "--provider", "openai"}); code != 0 {
		t.Fatalf("code %d err %q", code, errb.String())
	}
	m, ok := env.Profile.Models["pub"]
	if !ok {
		t.Fatal("model missing from the profile")
	}
	p, _ := config.Provider("openai")
	if m.Endpoint != p.Endpoint || m.APIKeyEnv != "OPENAI_API_KEY" || m.Context != p.Context || m.MaxTokensField != "max_completion_tokens" || m.SendsTemperature() {
		t.Fatalf("model %+v", m)
	}
	if !strings.Contains(out.String(), "OPENAI_API_KEY") || !strings.Contains(out.String(), "/set key pub") {
		t.Fatalf("output %q", out.String())
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"endpoint: https://api.openai.com/v1", "api_key_env: OPENAI_API_KEY", "max_tokens_field: max_completion_tokens", "send_temperature: \"false\""} {
		if !strings.Contains(string(saved), want) {
			t.Fatalf("config file missing %q:\n%s", want, saved)
		}
	}
}

func TestSetModelAddUnknownProvider(t *testing.T) {
	env, _, errb := testEnv(t)
	if code := runSet(context.Background(), env, []string{"model", "add", "pub", "--provider", "nope"}); code != 2 {
		t.Fatalf("code %d", code)
	}
	for _, name := range config.ProviderNames() {
		if !strings.Contains(errb.String(), name) {
			t.Fatalf("stderr does not list %s: %q", name, errb.String())
		}
	}
}
