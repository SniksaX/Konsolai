package commands

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// doctorProbeKey assembles a secret-shaped literal at runtime.
func doctorProbeKey() string { return "sk" + "-doctor-" + "0a1b2c3d" }

func doctorEnvWithKey(t *testing.T, endpoint, key string) (*Env, *strings.Builder) {
	t.Helper()
	restoreDoctorHooks(t)
	doctorLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	env, out, home := doctorEnv(t, endpoint)
	t.Setenv("HOME", home)
	doctorHome = func() (string, error) { return home, nil }
	envVar := "KONSOLAI_DOCTOR_PROBE_KEY"
	t.Setenv(envVar, key)
	name := roleModelName(env.Profile, "main")
	m := env.Profile.Models[name]
	m.APIKeyEnv = envVar
	env.Profile.Models[name] = m
	return env, out
}

func doctorLine(out, name string) string {
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, name) {
			return ln
		}
	}
	return ""
}

func TestDoctorModelProbeSendsKey(t *testing.T) {
	key := doctorProbeKey()
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			auth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-flash"}]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	env, out := doctorEnvWithKey(t, srv.URL+"/v1", key)
	if code := runDoctor(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	if auth != "Bearer "+key {
		t.Fatalf("/doctor did not send the key: %q", auth)
	}
	line := doctorLine(out.String(), "main endpoint")
	if !strings.Contains(line, "deepseek-flash") {
		t.Fatalf("main endpoint line %q", line)
	}
}

func TestDoctorAuthFailureWarnsAndNamesTheKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	env, out := doctorEnvWithKey(t, srv.URL+"/v1", doctorProbeKey())
	if code := runDoctor(context.Background(), env, nil); code != 0 {
		t.Fatalf("a 401 must warn, not fail: exit %d\n%s", code, out.String())
	}
	line := doctorLine(out.String(), "main endpoint")
	if !strings.Contains(line, "auth failed") || !strings.Contains(line, "!") {
		t.Fatalf("main endpoint line %q", line)
	}
	if strings.Contains(line, "unreachable") {
		t.Fatalf("a 401 read as unreachable: %q", line)
	}
	if !strings.Contains(out.String(), "KONSOLAI_DOCTOR_PROBE_KEY") {
		t.Fatalf("fix does not name the key variable\n%s", out.String())
	}
}

func TestDoctorHTTPStatusWarns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	env, out := doctorEnvWithKey(t, srv.URL+"/v1", doctorProbeKey())
	if code := runDoctor(context.Background(), env, nil); code != 0 {
		t.Fatalf("an HTTP status must warn, not fail: exit %d\n%s", code, out.String())
	}
	line := doctorLine(out.String(), "main endpoint")
	if !strings.Contains(line, "http 500") {
		t.Fatalf("main endpoint line %q", line)
	}
}

func TestDoctorUnreachableFails(t *testing.T) {
	env, out := doctorEnvWithKey(t, "http://127.0.0.1:1/v1", doctorProbeKey())
	if code := runDoctor(context.Background(), env, nil); code != 1 {
		t.Fatalf("a refused connection must fail: exit %d\n%s", code, out.String())
	}
	line := doctorLine(out.String(), "main endpoint")
	if !strings.Contains(line, "unreachable") || !strings.Contains(line, "✗") {
		t.Fatalf("main endpoint line %q", line)
	}
}

func TestDoctorSlowEndpointWarns(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(slow.Close)

	env, out := doctorEnvWithKey(t, slow.URL+"/v1", doctorProbeKey())
	env.Profile.UI.ProbeTimeoutMS = 40
	if code := runDoctor(context.Background(), env, nil); code != 0 {
		t.Fatalf("a slow endpoint must warn, not fail: exit %d\n%s", code, out.String())
	}
	line := doctorLine(out.String(), "main endpoint")
	if !strings.Contains(line, "slow") {
		t.Fatalf("main endpoint line %q", line)
	}
}

func TestDoctorThinkingProbeSendsKey(t *testing.T) {
	key := doctorProbeKey()
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"alpha"}]}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			auth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	env, out := doctorEnvWithKey(t, srv.URL+"/v1", key)
	env.Profile.SetMain(srv.URL+"/v1", "alpha", true)
	if code := runDoctor(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	if auth != "Bearer "+key {
		t.Fatalf("the thinking probe did not send the key: %q", auth)
	}
	line := doctorLine(out.String(), "thinking kwargs")
	if !strings.Contains(line, "accepted") {
		t.Fatalf("thinking line %q", line)
	}
}
