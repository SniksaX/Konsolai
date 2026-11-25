package commands

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

// statusProbeKey assembles a secret-shaped literal at runtime.
func statusProbeKey() string { return "sk" + "-status-" + "0a1b2c3d" }

func statusEnvWithKey(t *testing.T, endpoint, key string) (*Env, *bytes.Buffer) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	env, out, _ := testEnv(t)
	env.Profile = config.WithMain(endpoint, "")
	envVar := "KONSOLAI_STATUS_PROBE_KEY"
	t.Setenv(envVar, key)
	name := roleModelName(env.Profile, "main")
	m := env.Profile.Models[name]
	m.APIKeyEnv = envVar
	env.Profile.Models[name] = m
	return env, out
}

func TestStatusProbeSendsKey(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-flash","max_model_len":128000}]}`))
	}))
	t.Cleanup(srv.Close)

	env, out := statusEnvWithKey(t, srv.URL+"/v1", statusProbeKey())
	if code := runStatus(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if auth != "Bearer "+statusProbeKey() {
		t.Fatalf("/status did not send the key: %q", auth)
	}
	if line := statusLine(t, out.String(), "endpoint "); !strings.Contains(line, "ok ") {
		t.Fatalf("endpoint line %q", line)
	}
	if line := statusLine(t, out.String(), "model "); !strings.Contains(line, "deepseek-flash") {
		t.Fatalf("single served model not resolved: %q", line)
	}
}

func TestStatusAuthFailureSaysAuthFailed(t *testing.T) {
	key := statusProbeKey()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key ` + key + `"}}`))
	}))
	t.Cleanup(srv.Close)

	env, out := statusEnvWithKey(t, srv.URL+"/v1", key)
	if code := runStatus(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	line := statusLine(t, out.String(), "endpoint ")
	if !strings.Contains(line, "auth failed") {
		t.Fatalf("401 endpoint line %q", line)
	}
	if strings.Contains(line, "unreachable") {
		t.Fatalf("401 read as unreachable: %q", line)
	}
	if strings.Contains(out.String(), key) {
		t.Fatal("the status output leaked the key")
	}
	if line := statusLine(t, out.String(), "model "); !strings.Contains(line, "-") {
		t.Fatalf("model line %q", line)
	}
}

func TestStatusNoKeySendsNoAuthorizationLocally(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"local-model"}]}`))
	}))
	t.Cleanup(srv.Close)

	t.Setenv("HOME", t.TempDir())
	env, out, _ := testEnv(t)
	env.Profile = config.WithMain(srv.URL+"/v1", "")
	if code := runStatus(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if auth != "" {
		t.Fatalf("a local model without a key sent %q", auth)
	}
	if line := statusLine(t, out.String(), "model "); !strings.Contains(line, "local-model") {
		t.Fatalf("model line %q", line)
	}
}

func TestStatusAmbiguousModelListNamesServedIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-chat"},{"id":"deepseek-reasoner"}]}`))
	}))
	t.Cleanup(srv.Close)

	t.Setenv("HOME", t.TempDir())
	env, out, _ := testEnv(t)
	env.Profile = config.WithMain(srv.URL+"/v1", "")
	if code := runStatus(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	line := statusLine(t, out.String(), "model ")
	if !strings.Contains(line, "server serves:") || !strings.Contains(line, "deepseek-chat") || !strings.Contains(line, "deepseek-reasoner") {
		t.Fatalf("ambiguous list line %q", line)
	}
	if !strings.Contains(line, "model          -") {
		t.Fatalf("an ambiguous list displayed an id: %q", line)
	}
}

func TestStatusDoesNotDisplayAnUnservedConfiguredID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-flash"}]}`))
	}))
	t.Cleanup(srv.Close)

	t.Setenv("HOME", t.TempDir())
	env, out, _ := testEnv(t)
	env.Profile = config.WithMain(srv.URL+"/v1", "deepseek")
	if code := runStatus(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	line := statusLine(t, out.String(), "model ")
	if !strings.Contains(line, "deepseek") || !strings.Contains(line, "not served") || !strings.Contains(line, "deepseek-flash") {
		t.Fatalf("unserved configured id line %q", line)
	}
}

func TestStatusResolvesOnlyOneServedModelWhenIDIsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-flash"}]}`))
	}))
	t.Cleanup(srv.Close)

	t.Setenv("HOME", t.TempDir())
	env, out, _ := testEnv(t)
	env.Profile = config.WithMain(srv.URL+"/v1", "")
	if code := runStatus(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	line := statusLine(t, out.String(), "model ")
	if !strings.HasSuffix(strings.TrimSpace(line), "deepseek-flash") {
		t.Fatalf("single served model line %q", line)
	}
	if strings.Contains(line, "server serves:") {
		t.Fatalf("a single served model was reported ambiguous: %q", line)
	}
}
