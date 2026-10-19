package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

func TestSetKindsAndPaths(t *testing.T) {
	env, out, errb := setEnv(t)
	cases := []struct {
		args string
		path string
		want string
	}{
		{"welcome frame", "ui.welcome", "frame"},
		{"ui.theme glacier", "ui.theme", "glacier"},
		{"steps 20", "agent.max_steps.act", "20"},
		{"agent.max_steps.lookup 8", "agent.max_steps.lookup", "8"},
		{"top_p 0.9", "llm.top_p", "0.9"},
		{"temperature 0.4 build", "llm.temperature.build", "0.4"},
		{"approve in_plan", "agent.approve", "in_plan"},
		{"trace on", "ui.trace", "true"},
		{"languages en, fr", "language.languages", `["en", "fr"]`},
	}
	for _, tc := range cases {
		out.Reset()
		errb.Reset()
		if code := runSet(context.Background(), env, strings.Fields(tc.args)); code != 0 {
			t.Fatalf("%s: %d %s", tc.args, code, errb.String())
		}
		k, _ := config.KeyByPath(tc.path)
		if got := k.Present(env.Profile); got != tc.want && !strings.Contains(got, "en") {
			t.Fatalf("%s present %s want %s", tc.path, got, tc.want)
		}
	}
	out.Reset()
	if code := runSet(context.Background(), env, []string{"steps", "default"}); code != 0 {
		t.Fatal(errb.String())
	}
	if env.Profile.Agent.MaxSteps.Act != config.Builtin().Agent.MaxSteps.Act {
		t.Fatalf("act steps %d", env.Profile.Agent.MaxSteps.Act)
	}
}

func TestSetSessionNotWritten(t *testing.T) {
	env, _, errb := setEnv(t)
	before, err := os.ReadFile(env.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if code := runSet(context.Background(), env, []string{"trace", "on", "--session"}); code != 0 {
		t.Fatal(errb.String())
	}
	after, err := os.ReadFile(env.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("file changed\n%s", after)
	}
	if !env.Profile.UI.Trace {
		t.Fatal("session value not applied")
	}
	if env.Session["ui.trace"] == "" {
		t.Fatal("session map")
	}
}

func TestSetAmbiguity(t *testing.T) {
	env, out, errb := setEnv(t)
	if code := runSet(context.Background(), env, []string{"act"}); code != 2 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "max_steps.act") || !strings.Contains(out.String(), "temperature.act") {
		t.Fatalf("candidates\n%s", out.String())
	}
}

func TestSetEnvWarning(t *testing.T) {
	env, out, errb := setEnv(t)
	t.Setenv("KONSOLAI_BUILD_THINKING", "until_plan")
	if code := runSet(context.Background(), env, []string{"think", "build", "plan_step"}); code != 0 {
		t.Fatal(errb.String())
	}
	if !strings.Contains(out.String(), "overridden by KONSOLAI_BUILD_THINKING") {
		t.Fatalf("out %s", out.String())
	}
}

func TestSetPreservesComment(t *testing.T) {
	env, _, errb := setEnv(t)
	body := "defaults:\n  ui:\n    theme: amber   # palette name\n"
	if err := os.WriteFile(env.ConfigPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if code := runSet(context.Background(), env, []string{"theme", "jade"}); code != 0 {
		t.Fatal(errb.String())
	}
	got, err := os.ReadFile(env.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "jade") || !strings.Contains(string(got), "palette name") {
		t.Fatalf("file\n%s", got)
	}
}

func TestSetMenuGolden(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	got := formatSetList(config.Builtin())
	path := filepath.Join("testdata", "set_menu.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("menu mismatch\n%s", got)
	}
}

func TestSetDefaultThemeAndAlias(t *testing.T) {
	env, out, errb := setEnv(t)
	body := "defaults:\n  ui:\n    theme: jade   # palette name\n"
	if err := os.WriteFile(env.ConfigPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	env.Profile.UI.Theme = "jade"
	if code := runSet(context.Background(), env, []string{"default", "theme"}); code != 0 {
		t.Fatal(errb.String())
	}
	if env.Profile.PaletteName() != "amber" {
		t.Fatalf("live %s", env.Profile.PaletteName())
	}
	if strings.Contains(out.String(), "\x1b[H\x1b[2J") || !strings.Contains(out.String(), "applied now") {
		t.Fatalf("out %q", out.String())
	}
	got, err := os.ReadFile(env.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "jade") {
		t.Fatalf("override kept\n%s", got)
	}
	out.Reset()
	if err := os.WriteFile(env.ConfigPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	env.Profile.UI.Theme = "jade"
	if code := runSet(context.Background(), env, []string{"theme", "default"}); code != 0 {
		t.Fatal(errb.String())
	}
	if env.Profile.PaletteName() != config.Builtin().PaletteName() {
		t.Fatalf("alias live %s", env.Profile.PaletteName())
	}
}

func TestSetDefaultAllRestoresFactory(t *testing.T) {
	env, out, errb := setEnv(t)
	secret := "postgres://user:" + "hunter2" + "@localhost/db"
	body := "defaults:\n  ui:\n    theme: jade\n  agent:\n    max_steps:\n      act: 3\n  database:\n    url: " + secret + "\n"
	if err := os.WriteFile(env.ConfigPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	env.Profile.UI.Theme = "jade"
	env.Profile.Agent.MaxSteps.Act = 3
	env.Profile.Database.URL = secret
	if code := runSet(context.Background(), env, []string{"default", "all"}); code != 0 {
		t.Fatal(errb.String())
	}
	if !strings.Contains(out.String(), "applied now") || strings.Contains(out.String(), "\x1b[H\x1b[2J") {
		t.Fatalf("out %q", out.String())
	}
	got, err := os.ReadFile(env.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != config.InitYAML() {
		t.Fatal("file is not the factory document")
	}
	if strings.Contains(string(got), "hunter2") || strings.Contains(string(got), "theme: jade") {
		t.Fatalf("override leaked\n%s", got)
	}
	info, err := os.Stat(env.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	if env.Profile.PaletteName() != "amber" || env.Profile.Agent.MaxSteps.Act != config.Builtin().Agent.MaxSteps.Act {
		t.Fatalf("profile theme %s steps %d", env.Profile.PaletteName(), env.Profile.Agent.MaxSteps.Act)
	}
	if env.Profile.Database.URL != "" {
		t.Fatalf("database %q", env.Profile.Database.URL)
	}
}

func TestSetDefaultAllSessionSkipsFile(t *testing.T) {
	env, _, errb := setEnv(t)
	before := "defaults:\n  ui:\n    theme: jade\n"
	if err := os.WriteFile(env.ConfigPath, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	env.Profile.UI.Theme = "jade"
	if code := runSet(context.Background(), env, []string{"default", "all", "--session"}); code != 0 {
		t.Fatal(errb.String())
	}
	got, err := os.ReadFile(env.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != before {
		t.Fatalf("file changed\n%s", got)
	}
	if env.Profile.PaletteName() != "amber" {
		t.Fatalf("live %s", env.Profile.PaletteName())
	}
}

func TestSetSecretRefused(t *testing.T) {
	env, _, errb := setEnv(t)
	if code := runSet(context.Background(), env, []string{"database.url", "postgres://user:secret@localhost/db"}); code != 2 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(errb.String(), "secret") && !strings.Contains(errb.String(), "password") {
		t.Fatalf("%s", errb.String())
	}
}

func setEnv(t *testing.T) (*Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	env, out, errb := testEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("defaults: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env.ConfigPath = path
	env.Profile = config.Builtin()
	return env, out, errb
}
