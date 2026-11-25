package commands

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

// setProbeKey assembles a secret-shaped literal at runtime.
func setProbeKey() string { return "sk" + "-set-" + "0a1b2c3d" }

func TestSetModelTableSendsKeyAndShowsServerID(t *testing.T) {
	key := setProbeKey()
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-flash","max_model_len":128000}]}`))
	}))
	t.Cleanup(srv.Close)

	t.Setenv("HOME", t.TempDir())
	env, out, _ := testEnv(t)
	env.Profile = config.WithMain(srv.URL+"/v1", "")
	envVar := "KONSOLAI_SET_PROBE_KEY"
	t.Setenv(envVar, key)
	m := env.Profile.Models["default"]
	m.APIKeyEnv = envVar
	env.Profile.Models["default"] = m

	if code := runSet(context.Background(), env, []string{"model"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if auth != "Bearer "+key {
		t.Fatalf("/set model did not send the key: %q", auth)
	}
	text := out.String()
	if !strings.Contains(text, "deepseek-flash") {
		t.Fatalf("the served id is missing from\n%s", text)
	}
	if !strings.Contains(text, "up") {
		t.Fatalf("the reachable row is not up\n%s", text)
	}
}

func TestSetModelTableAuthFailureReadsAsAuthFailed(t *testing.T) {
	key := setProbeKey()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key ` + key + `"}}`))
	}))
	t.Cleanup(srv.Close)

	t.Setenv("HOME", t.TempDir())
	env, out, _ := testEnv(t)
	env.Profile = config.WithMain(srv.URL+"/v1", "")
	envVar := "KONSOLAI_SET_PROBE_KEY"
	t.Setenv(envVar, key)
	m := env.Profile.Models["default"]
	m.APIKeyEnv = envVar
	env.Profile.Models["default"] = m

	if code := runSet(context.Background(), env, []string{"model"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	text := out.String()
	if !strings.Contains(text, "auth failed") {
		t.Fatalf("a 401 must read as auth failed\n%s", text)
	}
	if strings.Contains(text, "unreachable") || strings.Contains(text, " down") {
		t.Fatalf("a 401 read as down\n%s", text)
	}
	if strings.Contains(text, key) {
		t.Fatal("the model table leaked the key")
	}
}

func TestSetModelTableSendsNoKeyToAKeylessLocalModel(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"local-model"}]}`))
	}))
	t.Cleanup(srv.Close)

	t.Setenv("HOME", t.TempDir())
	env, _, _ := testEnv(t)
	env.Profile = config.WithMain(srv.URL+"/v1", "")
	if code := runSet(context.Background(), env, []string{"model"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if auth != "" {
		t.Fatalf("a local model without a key sent %q", auth)
	}
}

func TestSetModelDiscoverSendsKey(t *testing.T) {
	key := setProbeKey()
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"new-model","max_model_len":8192}]}`))
	}))
	t.Cleanup(srv.Close)

	t.Setenv("HOME", t.TempDir())
	env, out, _ := testEnv(t)
	env.Profile = config.WithMain(srv.URL+"/v1", "")
	envVar := "KONSOLAI_SET_PROBE_KEY"
	t.Setenv(envVar, key)
	m := env.Profile.Models["default"]
	m.APIKeyEnv = envVar
	env.Profile.Models["default"] = m

	if code := runSet(context.Background(), env, []string{"model", "discover"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if auth != "Bearer "+key {
		t.Fatalf("/set model discover did not send the key: %q", auth)
	}
	if text := out.String(); !strings.Contains(text, "/set model add new-model") {
		t.Fatalf("discover output\n%s", text)
	}
}
