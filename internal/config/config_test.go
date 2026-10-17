package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"KONSOLAI_PROFILE", "KONSOLAI_DATABASE_URL", "KONSOLAI_ENDPOINT",
		"KONSOLAI_MODEL", "KONSOLAI_FAST_ENDPOINT", "KONSOLAI_FAST_MODEL",
		"KONSOLAI_BUILD_THINKING", "KONSOLAI_EMBED_ENDPOINT", "KONSOLAI_RERANK_ENDPOINT",
	} {
		t.Setenv(k, "")
	}
}

func TestLoadMissingFile(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if f.DefaultProfile != "" || len(f.Profiles) != 0 {
		t.Fatalf("got %+v", f)
	}
}

func TestLoadInvalidYAMLMentionsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("default_profile: ["), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("want path in error, got %v", err)
	}
}

func TestLoadUnknownFieldRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("extra: true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected unknown field error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("want path in error, got %v", err)
	}
	if !strings.Contains(err.Error(), `"extra"`) || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("want key and line, got %v", err)
	}
}

func TestLoadUnknownProfileFieldRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "profiles:\n  home:\n    nope: 1\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected unknown field error")
	}
	if !strings.Contains(err.Error(), `"profiles.home.nope"`) || !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("want nested key and line, got %v", err)
	}
}

func TestUnknownNestedKeyLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "defaults:\n  llm:\n    endpoint: http://x/v1\n    mystery: 1\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), `"defaults.llm.mystery"`) || !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("got %v", err)
	}
}

func TestResolveBuiltinDefault(t *testing.T) {
	clearEnv(t)
	p, err := File{}.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "default" || p.Endpoint != defaultEndpoint {
		t.Fatalf("got %+v", p)
	}
	if p.SessionIdleMinutes != 30 || p.RetentionDays != 90 || p.LLMIdleTimeoutSeconds != 120 {
		t.Fatalf("defaults %+v", p)
	}
	if p.BuildThinking != "plan_step" {
		t.Fatalf("build_thinking %q", p.BuildThinking)
	}
	if p.FastEndpoint != defaultFastEndpoint || p.FastModel != "" {
		t.Fatalf("fast %+v", p)
	}
	if p.EmbedEndpoint != defaultEmbedEndpoint || p.RerankEndpoint != defaultRerankEndpoint {
		t.Fatalf("rag endpoints %+v", p)
	}
	if !p.RAGEnabled() {
		t.Fatal("memory_rag default")
	}
	if len(p.Languages) != 2 || p.Languages[0] != "en" || p.Languages[1] != "fr" {
		t.Fatalf("languages %+v", p.Languages)
	}
	if p.DefaultLanguage != "en" {
		t.Fatalf("default_language %q", p.DefaultLanguage)
	}
	b := Builtin()
	if p.LLM.MaxTokens.Build != b.LLM.MaxTokens.Build || p.Agent.MaxSteps.Lookup != 10 {
		t.Fatalf("nested defaults %+v", p)
	}
}

func TestResolveLanguageFields(t *testing.T) {
	clearEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "profiles:\n  default:\n    language:\n      languages: [fr, en]\n      default: fr\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.Resolve("default")
	if err != nil {
		t.Fatal(err)
	}
	if p.DefaultLanguage != "fr" || len(p.Languages) != 2 || p.Languages[0] != "fr" {
		t.Fatalf("got %+v", p)
	}
	p, err = File{Profiles: map[string]Overlay{"default": {Language: &LanguageOverlay{Default: strPtr("es")}}}}.Resolve("default")
	if err != nil {
		t.Fatal(err)
	}
	if p.DefaultLanguage != "en" {
		t.Fatalf("es clamped to list, got %q", p.DefaultLanguage)
	}
}

func TestResolveFastEnv(t *testing.T) {
	t.Setenv("KONSOLAI_FAST_ENDPOINT", "http://fast/v1")
	t.Setenv("KONSOLAI_FAST_MODEL", "mini")
	p, err := File{}.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if p.FastEndpoint != "http://fast/v1" || p.FastModel != "mini" {
		t.Fatalf("got %+v", p)
	}
}

func TestResolveEmbedRerankEnv(t *testing.T) {
	t.Setenv("KONSOLAI_EMBED_ENDPOINT", "http://embed:9")
	t.Setenv("KONSOLAI_RERANK_ENDPOINT", "http://rerank:9")
	p, err := File{}.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if p.EmbedEndpoint != "http://embed:9" || p.RerankEndpoint != "http://rerank:9" {
		t.Fatalf("got %+v", p)
	}
}

func TestResolveMemoryRAGFalse(t *testing.T) {
	clearEnv(t)
	off := false
	p, err := File{Profiles: map[string]Overlay{"default": {RAG: &RAGOverlay{Enabled: &off}}}}.Resolve("default")
	if err != nil {
		t.Fatal(err)
	}
	if p.RAGEnabled() {
		t.Fatal("want disabled")
	}
}

func TestResolveBuildThinkingEnv(t *testing.T) {
	t.Setenv("KONSOLAI_BUILD_THINKING", "until_plan")
	p, err := File{}.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if p.BuildThinking != "until_plan" {
		t.Fatalf("got %q", p.BuildThinking)
	}
}

func TestResolveUnknownProfile(t *testing.T) {
	_, err := File{}.Resolve("nope")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("got %v", err)
	}
}

func TestResolvePrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `
default_profile: home
defaults:
  llm:
    model: defaults-model
    idle_timeout_seconds: 90
  memory:
    retention_days: 3
profiles:
  home:
    database:
      url: postgres://h/home
    llm:
      endpoint: http://home/v1
      model: home-model
    memory:
      session_idle_minutes: 10
      retention_days: 7
  work:
    database:
      url: postgres://w/work
    llm:
      endpoint: http://work/v1
      model: work-model
`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("default_profile", func(t *testing.T) {
		clearEnv(t)
		p, err := f.Resolve("")
		if err != nil {
			t.Fatal(err)
		}
		if p.Name != "home" || p.Model != "home-model" || p.Endpoint != "http://home/v1" {
			t.Fatalf("got %+v", p)
		}
		if p.SessionIdleMinutes != 10 || p.RetentionDays != 7 {
			t.Fatalf("got %+v", p)
		}
		if p.LLM.IdleTimeoutSeconds != 90 {
			t.Fatalf("defaults not applied: %d", p.LLM.IdleTimeoutSeconds)
		}
	})

	t.Run("env profile beats default_profile", func(t *testing.T) {
		t.Setenv("KONSOLAI_PROFILE", "work")
		t.Setenv("KONSOLAI_DATABASE_URL", "")
		t.Setenv("KONSOLAI_ENDPOINT", "")
		t.Setenv("KONSOLAI_MODEL", "")
		p, err := f.Resolve("")
		if err != nil {
			t.Fatal(err)
		}
		if p.Name != "work" || p.Model != "work-model" {
			t.Fatalf("got %+v", p)
		}
		if p.RetentionDays != 3 {
			t.Fatalf("defaults retention %d", p.RetentionDays)
		}
	})

	t.Run("named profile beats env", func(t *testing.T) {
		t.Setenv("KONSOLAI_PROFILE", "work")
		t.Setenv("KONSOLAI_DATABASE_URL", "")
		t.Setenv("KONSOLAI_ENDPOINT", "")
		t.Setenv("KONSOLAI_MODEL", "")
		p, err := f.Resolve("home")
		if err != nil {
			t.Fatal(err)
		}
		if p.Name != "home" || p.Model != "home-model" {
			t.Fatalf("got %+v", p)
		}
	})

	t.Run("env fields beat profile", func(t *testing.T) {
		t.Setenv("KONSOLAI_PROFILE", "")
		t.Setenv("KONSOLAI_DATABASE_URL", "postgres://env/db")
		t.Setenv("KONSOLAI_ENDPOINT", "http://env/v1")
		t.Setenv("KONSOLAI_MODEL", "env-model")
		p, err := f.Resolve("home")
		if err != nil {
			t.Fatal(err)
		}
		if p.DatabaseURL != "postgres://env/db" || p.Endpoint != "http://env/v1" || p.Model != "env-model" {
			t.Fatalf("got %+v", p)
		}
		if p.Location() != "env/db" {
			t.Fatalf("location %q", p.Location())
		}
	})
}

func TestValidateRatiosAndElide(t *testing.T) {
	clearEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("defaults:\n  context:\n    elide_trigger: 1.5\n"), 0644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Resolve("")
	if err == nil || !strings.Contains(err.Error(), "elide_trigger") || !strings.Contains(err.Error(), "(0,1)") {
		t.Fatalf("got %v", err)
	}

	if err := os.WriteFile(path, []byte("defaults:\n  context:\n    elide_trigger: 0.3\n    elide_target: 0.5\n"), 0644); err != nil {
		t.Fatal(err)
	}
	f, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Resolve("")
	if err == nil || !strings.Contains(err.Error(), "elide_target") || !strings.Contains(err.Error(), "elide_trigger") {
		t.Fatalf("got %v", err)
	}

	if err := os.WriteFile(path, []byte("defaults:\n  agent:\n    max_steps:\n      lookup: 0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	f, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Resolve("")
	if err == nil || !strings.Contains(err.Error(), "max_steps.lookup") {
		t.Fatalf("got %v", err)
	}

	if err := os.WriteFile(path, []byte("defaults:\n  llm:\n    endpoint: not-a-url\n"), 0644); err != nil {
		t.Fatal(err)
	}
	f, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Resolve("")
	if err == nil || !strings.Contains(err.Error(), "llm.endpoint") {
		t.Fatalf("got %v", err)
	}
}

func TestInvalidTheme(t *testing.T) {
	c := Builtin()
	c.UI.Theme = "neon"
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "ui.theme") {
		t.Fatalf("got %v", err)
	}
	c.UI.Theme = "amber"
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"glacier", "nebula", "jade", "rose", "mono", "ocean", "violet", "forest", "cyan", "green", "red"} {
		c.UI.Theme = name
		if err := Validate(c); err != nil {
			t.Fatal(err)
		}
	}
	c.UI.Theme = "amber"
	c.UI.Appearance = "sepia"
	err = Validate(c)
	if err == nil || !strings.Contains(err.Error(), "ui.appearance") {
		t.Fatalf("got %v", err)
	}
}

func TestMigrateJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	clearEnv(t)
	cfgDir := filepath.Join(dir, "konsolai")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(cfgDir, "config.json")
	body := `{
		"default_profile": "home",
		"profiles": {
			"home": {
				"database_url": "postgres://u:secret@localhost:5432/k",
				"endpoint": "http://home/v1",
				"model": "home-model",
				"session_idle_minutes": 12,
				"memory_rag": false
			}
		}
	}`
	if err := os.WriteFile(jsonPath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	f, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if f.Notice == "" || !strings.Contains(f.Notice, "migrated") {
		t.Fatalf("notice %q", f.Notice)
	}
	yamlPath := DefaultPath()
	if _, err := os.Stat(yamlPath); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("mode %o", fi.Mode().Perm())
	}
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Fatal("json should be renamed")
	}
	if _, err := os.Stat(jsonPath + ".bak"); err != nil {
		t.Fatal(err)
	}
	p, err := f.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "home" || p.Model != "home-model" || p.Endpoint != "http://home/v1" {
		t.Fatalf("got %+v", p)
	}
	if p.SessionIdleMinutes != 12 || p.RAGEnabled() {
		t.Fatalf("got %+v rag=%v", p, p.RAGEnabled())
	}
	if _, err := os.Stat(yamlPath); err == nil {
		f2, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if f2.Notice != "" {
			t.Fatalf("second load migrated again: %q", f2.Notice)
		}
	}
}

func TestShowMasksDatabaseURL(t *testing.T) {
	clearEnv(t)
	c := Builtin()
	c.Name = "home"
	c.Database.URL = "postgres://user:hunter2@localhost:5432/konsolai"
	c.syncFlat()
	got, err := c.ShowYAML()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "hunter2") {
		t.Fatalf("leaked password: %s", got)
	}
	if !strings.Contains(got, "***") || !strings.Contains(got, "profile: home") {
		t.Fatalf("got %s", got)
	}
}

func TestInitParsesToDefaults(t *testing.T) {
	clearEnv(t)
	raw := InitYAML()
	if !strings.Contains(raw, "default_profile:") {
		t.Fatal(raw)
	}
	f, err := Parse([]byte(raw), "init.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.Resolve("home")
	if err != nil {
		t.Fatal(err)
	}
	want := Builtin()
	got.Name = want.Name
	gb, err := yaml.Marshal(showViewOf(got))
	if err != nil {
		t.Fatal(err)
	}
	wb, err := yaml.Marshal(showViewOf(want))
	if err != nil {
		t.Fatal(err)
	}
	if string(gb) != string(wb) {
		t.Fatalf("init != builtin\n got:\n%s\nwant:\n%s", gb, wb)
	}
}

func showViewOf(c Config) showView {
	return showView{
		LLM: c.LLM, Fast: c.Fast, Database: c.Database, Memory: c.Memory,
		Context: c.Context, Agent: c.Agent, RAG: c.RAG, UI: c.UI,
		Language: c.Language, Safety: c.Safety, Router: c.Router,
	}
}

func TestWriteInitRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteInit(path, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("got %v", err)
	}
	if err := WriteInit(path, true); err != nil {
		t.Fatal(err)
	}
}

func TestLocationOmitsURL(t *testing.T) {
	p := Config{DatabaseURL: "postgres://user:secret@localhost:5432/konsolai"}
	loc := p.Location()
	if strings.Contains(loc, "secret") || strings.Contains(loc, "postgres://") {
		t.Fatalf("leaked URL: %q", loc)
	}
	if loc != "localhost:5432/konsolai" {
		t.Fatalf("got %q", loc)
	}
}

func TestDisableLine(t *testing.T) {
	err := errors.New("store: ping: failed to connect to `user=x database=x`:\n\t127.0.0.1:1 dial tcp: connection refused")
	got := DisableLine("postgres://secret:hunter2@127.0.0.1:1/konsolai", err)
	if strings.Contains(got, "secret") || strings.Contains(got, "hunter2") || strings.Contains(got, "user=x") {
		t.Fatalf("leaked: %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("want one line: %q", got)
	}
	if !strings.HasPrefix(got, "memory disabled: cannot connect to 127.0.0.1:1:") {
		t.Fatalf("prefix: %q", got)
	}
}

func TestDisableLineInnermost(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "connection refused",
			err:  fmt.Errorf("store: ping: %w", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}),
			want: "connection refused",
		},
		{
			name: "no such host",
			err:  fmt.Errorf("store: ping: %w", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("no such host")}),
			want: "no such host",
		},
		{
			name: "timeout",
			err:  fmt.Errorf("store: ping: %w", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("i/o timeout")}),
			want: "i/o timeout",
		},
		{
			name: "password",
			err:  fmt.Errorf("store: ping: %w", errors.New("password authentication failed")),
			want: "password authentication failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DisableLine("postgres://u:p@127.0.0.1:1/x", tt.err)
			if strings.Contains(got, "u:p") || strings.Contains(got, "\n") {
				t.Fatalf("leaked: %q", got)
			}
			if !strings.Contains(got, tt.want) {
				t.Fatalf("%q want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultPathUsesXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	got := DefaultPath()
	if got != "/tmp/xdg/konsolai/config.yaml" {
		t.Fatalf("got %q", got)
	}
}

func TestTunablesOmitAndOverride(t *testing.T) {
	clearEnv(t)
	p, err := File{}.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if p.MaxStepsFor("act") != 15 || p.ProbeDeadline() != 400*time.Millisecond || p.WelcomeLayout() != "split" {
		t.Fatalf("builtin steps=%d probe=%s welcome=%s", p.MaxStepsFor("act"), p.ProbeDeadline(), p.WelcomeLayout())
	}
	if p.IdenticalRejectLimit() != 2 || p.ProbeTimeout() != 300*time.Millisecond ||
		p.GitQueryTimeout() != 200*time.Millisecond || p.MemoryQueryTimeout() != 200*time.Millisecond ||
		p.StartupCap() != 500*time.Millisecond || p.WelcomeCacheFor() != 60*time.Second ||
		p.ModelRateWindow() != 20 || p.CtxWarnPct() != 60 || p.CtxHotPct() != 85 ||
		p.CtxCells() != 8 || p.CtxNarrowCols() != 90 ||
		p.LayoutTinyHeight() != 22 || p.LayoutRecentHeight() != 30 ||
		p.LayoutTinyWidth() != 64 || p.LayoutNarrowWidth() != 110 || p.LayoutFrameWidth() != 120 {
		t.Fatalf("builtin tunables %+v", p.UI)
	}

	omit := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(omit, []byte("default_profile: home\nprofiles:\n  home: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(omit)
	if err != nil {
		t.Fatal(err)
	}
	p, err = f.Resolve("home")
	if err != nil {
		t.Fatal(err)
	}
	if p.MaxStepsFor("act") != 15 || p.ProbeDeadline() != 400*time.Millisecond || p.WelcomeLayout() != "split" || p.IdenticalRejectLimit() != 2 {
		t.Fatalf("omitted steps=%d probe=%s welcome=%s rejects=%d", p.MaxStepsFor("act"), p.ProbeDeadline(), p.WelcomeLayout(), p.IdenticalRejectLimit())
	}

	path := filepath.Join(t.TempDir(), "override.yaml")
	body := `defaults:
  agent:
    max_steps:
      act: 7
    identical_rejects: 4
  ui:
    welcome: minimal
    probe_deadline_ms: 250
    layout:
      tiny_height: 18
`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	f, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err = f.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if p.MaxStepsFor("act") != 7 {
		t.Fatalf("step cap %d", p.MaxStepsFor("act"))
	}
	if p.ProbeDeadline() != 250*time.Millisecond {
		t.Fatalf("probe %s", p.ProbeDeadline())
	}
	if p.WelcomeLayout() != "minimal" {
		t.Fatalf("welcome %s", p.WelcomeLayout())
	}
	if p.IdenticalRejectLimit() != 4 || p.LayoutTinyHeight() != 18 || p.LayoutFrameWidth() != 120 {
		t.Fatalf("partial override rejects=%d tiny=%d frame=%d", p.IdenticalRejectLimit(), p.LayoutTinyHeight(), p.LayoutFrameWidth())
	}

	raw := InitYAML()
	for _, key := range []string{
		"probe_deadline_ms: 400",
		"probe_timeout_ms: 300",
		"git_query_ms: 200",
		"memory_query_ms: 200",
		"startup_cap_ms: 500",
		"welcome_cache_seconds: 60",
		"model_rate_calls: 20",
		"identical_rejects: 2",
		"ctx_warn_pct: 60",
		"ctx_hot_pct: 85",
		"ctx_cells: 8",
		"ctx_narrow_cols: 90",
		"tiny_height: 22",
		"recent_height: 30",
		"tiny_width: 64",
		"narrow_width: 110",
		"frame_width: 120",
		"welcome: split",
		"act: 15",
	} {
		if !strings.Contains(raw, key) {
			t.Fatalf("init missing %s", key)
		}
	}
}

func TestWelcomeLayoutDefaultAndOverride(t *testing.T) {
	clearEnv(t)
	if Builtin().WelcomeLayout() != "split" {
		t.Fatalf("builtin %q", Builtin().WelcomeLayout())
	}
	if (Config{}).WelcomeLayout() != "split" {
		t.Fatal("empty should be split")
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "defaults:\n  ui:\n    welcome: frame\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if p.WelcomeLayout() != "frame" || p.UI.Welcome != "frame" {
		t.Fatalf("got %+v", p.UI.Welcome)
	}
	raw := InitYAML()
	if !strings.Contains(raw, "welcome: split") {
		t.Fatalf("init missing welcome:\n%s", raw)
	}
	p.UI.Welcome = "nope"
	if p.WelcomeLayout() != "split" {
		t.Fatalf("bad value %q", p.WelcomeLayout())
	}
	shown, err := Builtin().ShowYAML()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, "welcome: split") {
		t.Fatalf("show:\n%s", shown)
	}
}
