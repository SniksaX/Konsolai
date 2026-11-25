package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

func TestSetModelIDKeepsComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `defaults:
  models:
    big:
      endpoint: http://old.example/v1
      model: big-id   # keep this comment
  roles:
    main: big
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	env, out, errb := testEnv(t)
	env.ConfigPath = path
	env.Profile = config.WithMain("http://old.example/v1", "big-id")
	env.Profile.Models = map[string]config.Model{"big": {Endpoint: "http://old.example/v1", Model: "big-id"}}
	env.Profile.Roles.Main = "big"

	if code := runSet(context.Background(), env, []string{"model", "id", "big", "deepseek-flash"}); code != 0 {
		t.Fatalf("code %d, err %q", code, errb.String())
	}
	if !strings.Contains(out.String(), "from the next turn") {
		t.Fatalf("output %q", out.String())
	}
	if got := env.Profile.Models["big"].Model; got != "deepseek-flash" {
		t.Fatalf("profile id %q", got)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "model: deepseek-flash") {
		t.Fatalf("file not written:\n%s", saved)
	}
	if !strings.Contains(string(saved), "# keep this comment") {
		t.Fatalf("comments were lost:\n%s", saved)
	}
}

func TestSetModelIDRejects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("defaults:\n  models:\n    big:\n      endpoint: http://old/v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, _, errb := testEnv(t)
	env.ConfigPath = path
	env.Profile.Models = map[string]config.Model{"big": {Endpoint: "http://old/v1"}}

	if code := runSet(context.Background(), env, []string{"model", "id", "nope", "x"}); code != 2 {
		t.Fatalf("unknown model: code %d", code)
	}
	if !strings.Contains(errb.String(), `no model "nope"`) {
		t.Fatalf("error %q", errb.String())
	}
	if code := runSet(context.Background(), env, []string{"model", "id", "big"}); code != 2 {
		t.Fatalf("missing id: code %d", code)
	}
	if code := runSet(context.Background(), env, []string{"model", "id", "big", "   "}); code != 2 {
		t.Fatalf("blank id: code %d", code)
	}
	if !strings.Contains(errb.String(), "model id is empty") {
		t.Fatalf("blank id error %q", errb.String())
	}
}
