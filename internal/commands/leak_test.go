package commands

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/memory"
	"github.com/SniksaX/Konsolai/internal/store"
)

// The fake credential is assembled at runtime so a secret scanner never flags
// a literal, and it never matches a real key shape.
const leakKeyEnv = "KONSOLAI_LEAK_PROOF_KEY"

func leakKey() string {
	return "sk" + "-leak-" + "proof-" + "9f8e7d6c5b4a3210"
}

func leakProfile(endpoint string) (config.Config, string) {
	c := config.WithMain(endpoint, "leak-model")
	name := c.Roles.Main
	m := c.Models[name]
	m.APIKeyEnv = leakKeyEnv
	c.Models[name] = m
	return c, name
}

// setLeakKey points HOME at a temp dir in both modes, then stores the key where
// the surface under test reads it: the environment or the secrets file.
func setLeakKey(t *testing.T, mode, model, key string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	switch mode {
	case "env":
		t.Setenv(leakKeyEnv, key)
	case "secrets":
		if err := config.WriteSecret(model, key); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown key mode %q", mode)
	}
}

func requireLeakKey(t *testing.T, c config.Config, model, key string) {
	t.Helper()
	got, err := c.ModelAPIKey(model)
	if err != nil || got != key {
		t.Fatalf("the key did not resolve: %v", err)
	}
}

func assertNoLeak(t *testing.T, where, key, text string) {
	t.Helper()
	if strings.Contains(text, key) {
		t.Fatalf("%s leaked the model key", where)
	}
}

func leakModelsServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"leak-model","max_model_len":8192}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestStatusDoesNotLeakModelKey(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	srv := leakModelsServer(t)
	for _, mode := range []string{"env", "secrets"} {
		t.Run(mode, func(t *testing.T) {
			key := leakKey()
			env, out, errb := testEnv(t)
			env.Profile, _ = leakProfile(srv.URL + "/v1")
			setLeakKey(t, mode, env.Profile.Roles.Main, key)
			requireLeakKey(t, env.Profile, env.Profile.Roles.Main, key)
			if code := runStatus(context.Background(), env, nil); code != 0 {
				t.Fatalf("exit %d: %s", code, errb.String())
			}
			if !strings.Contains(out.String(), "leak-model") {
				t.Fatalf("status did not show the model: %q", out.String())
			}
			assertNoLeak(t, "/status stdout", key, out.String())
			assertNoLeak(t, "/status stderr", key, errb.String())
		})
	}
}

func TestDoctorDoesNotLeakModelKey(t *testing.T) {
	restoreDoctorHooks(t)
	doctorLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	doctorHome = func() (string, error) { return t.TempDir(), nil }
	st := leakModelsServer(t)
	for _, mode := range []string{"env", "secrets"} {
		t.Run(mode, func(t *testing.T) {
			key := leakKey()
			env, out, errb := testEnv(t)
			prof, _ := leakProfile(st.URL + "/v1")
			prof.Name = "default"
			prof.UI.ProbeTimeoutMS = 300
			env.Profile = prof
			cfgPath := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(cfgPath, []byte("profiles:\n  default: {}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			env.ConfigPath = cfgPath
			setLeakKey(t, mode, prof.Roles.Main, key)
			requireLeakKey(t, env.Profile, prof.Roles.Main, key)
			runDoctor(context.Background(), env, nil)
			text := out.String() + errb.String()
			if !strings.Contains(text, "main endpoint") || !strings.Contains(text, "leak-model") {
				t.Fatalf("doctor did not probe the endpoint: %q", text)
			}
			assertNoLeak(t, "/doctor stdout", key, out.String())
			assertNoLeak(t, "/doctor stderr", key, errb.String())
		})
	}
}

func TestConfigShowDoesNotLeakModelKey(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, mode := range []string{"env", "secrets"} {
		t.Run(mode, func(t *testing.T) {
			key := leakKey()
			env, out, errb := testEnv(t)
			env.Profile, _ = leakProfile("http://127.0.0.1:9/v1")
			setLeakKey(t, mode, env.Profile.Roles.Main, key)
			requireLeakKey(t, env.Profile, env.Profile.Roles.Main, key)
			if code := runConfig(context.Background(), env, []string{"show"}); code != 0 {
				t.Fatalf("exit %d: %s", code, errb.String())
			}
			if !strings.Contains(out.String(), "models:") || !strings.Contains(out.String(), leakKeyEnv) {
				t.Fatalf("/config show did not render the model: %q", out.String())
			}
			assertNoLeak(t, "/config show stdout", key, out.String())
			assertNoLeak(t, "/config show stderr", key, errb.String())
		})
	}
}

func TestConfigDiffDoesNotLeakModelKey(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, mode := range []string{"env", "secrets"} {
		t.Run(mode, func(t *testing.T) {
			key := leakKey()
			path := filepath.Join(t.TempDir(), "config.yaml")
			body := "default_profile: leak\ndefaults:\n  models:\n    default:\n" +
				"      endpoint: http://127.0.0.1:9/v1\n      model: leak-model\n" +
				"      api_key_env: " + leakKeyEnv + "\nprofiles:\n  leak: {}\n"
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			env, out, errb := testEnv(t)
			env.Profile, _ = leakProfile("http://127.0.0.1:9/v1")
			env.Profile.Name = "leak"
			env.ConfigPath = path
			setLeakKey(t, mode, env.Profile.Roles.Main, key)
			requireLeakKey(t, env.Profile, env.Profile.Roles.Main, key)
			if code := runConfig(context.Background(), env, []string{"diff"}); code != 0 {
				t.Fatalf("exit %d: %s", code, errb.String())
			}
			if !strings.Contains(out.String(), "models.default.") || !strings.Contains(out.String(), "(file)") {
				t.Fatalf("/config diff did not render the model diff: %q", out.String())
			}
			assertNoLeak(t, "/config diff stdout", key, out.String())
			assertNoLeak(t, "/config diff stderr", key, errb.String())
		})
	}
}

func TestStatsDoesNotLeakModelKey(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	raw := os.Getenv("KONSOLAI_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("KONSOLAI_TEST_DATABASE_URL not set")
	}
	st := openHistoryStore(t, raw)
	ctx := context.Background()
	for _, mode := range []string{"env", "secrets"} {
		t.Run(mode, func(t *testing.T) {
			key := leakKey()
			env, out, errb := testEnv(t)
			prof, _ := leakProfile("http://127.0.0.1:9/v1")
			prof.Name = "leakstat"
			env.Profile = prof
			env.Mem = memory.NewGuard(st, func(error) {})
			setLeakKey(t, mode, prof.Roles.Main, key)
			requireLeakKey(t, env.Profile, prof.Roles.Main, key)
			id, err := st.CreateSession(ctx, store.NewSession{
				Profile: prof.Name, CWD: t.TempDir(), Mode: "answer", Model: "leak-model", Title: "leak proof " + mode,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := st.RecordTurnStat(ctx, store.TurnStat{
				SessionID: id, Intent: "answer", Model: "leak-model", Endpoint: prof.MainEndpoint(),
				PromptTokens: 11, CompletionTokens: 5, ModelMS: 100,
			}); err != nil {
				t.Fatal(err)
			}
			if code := runStats(ctx, env, []string{"all"}); code != 0 {
				t.Fatalf("exit %d: %s", code, errb.String())
			}
			if !strings.Contains(out.String(), "leak-model") {
				t.Fatalf("/stats did not render the row: %q", out.String())
			}
			assertNoLeak(t, "/stats stdout", key, out.String())
			assertNoLeak(t, "/stats stderr", key, errb.String())
			rep, err := st.UsageReport(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			assertNoLeak(t, "the stats rows", key, fmt.Sprintf("%+v", rep))
		})
	}
}
