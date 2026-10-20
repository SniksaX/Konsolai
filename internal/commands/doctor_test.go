package commands

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func restoreDoctorHooks(t *testing.T) {
	t.Helper()
	look, home, term := doctorLookPath, doctorHome, doctorTermSize
	t.Cleanup(func() {
		doctorLookPath = look
		doctorHome = home
		doctorTermSize = term
	})
	doctorTermSize = func() (int, int) { return 80, 24 }
	t.Setenv("KONSOLAI_CHROME", "")
	t.Setenv("KONSOLAI_ENDPOINT", "")
	t.Setenv("KONSOLAI_DATABASE_URL", "")
	t.Setenv("KONSOLAI_PROFILE", "")
	t.Setenv("NO_COLOR", "1")
}

func doctorEnv(t *testing.T, endpoint string) (*Env, *strings.Builder, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("profiles:\n  default: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	env, _, _ := testEnv(t)
	env.Out = &out
	env.ConfigPath = path
	env.Profile.Name = "default"
	env.Profile.SetMain(endpoint, env.Profile.MainModelID(), env.Profile.MainThinking())
	env.Profile.UI.ProbeTimeoutMS = 300
	return env, &out, dir
}

func TestDoctorUp(t *testing.T) {
	restoreDoctorHooks(t)
	doctorLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"alpha"},{"id":"beta"}]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	env, out, home := doctorEnv(t, srv.URL+"/v1")
	doctorHome = func() (string, error) { return home, nil }
	env.Profile.SetFast("fast", srv.URL+"/v1", "", false)
	env.Profile.Roles.Embed = srv.URL
	env.Profile.Roles.Rerank = srv.URL
	if code := runDoctor(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	text := out.String()
	for _, want := range []string{"alpha, beta", "0600", "pgpass", "absent", "color=none", "width=80", "height=24", "NO_COLOR=on", "not configured", "fix: database.url"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q\n%s", want, text)
		}
	}
	if strings.Contains(text, "✗") {
		t.Fatalf("unexpected failure\n%s", text)
	}
}

func TestDoctorSlowAndDown(t *testing.T) {
	restoreDoctorHooks(t)
	doctorLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()
	env, out, home := doctorEnv(t, slow.URL+"/v1")
	doctorHome = func() (string, error) { return home, nil }
	env.Profile.UI.ProbeTimeoutMS = 40
	if code := runDoctor(context.Background(), env, nil); code != 1 {
		t.Fatalf("slow exit %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "timeout") || !strings.Contains(out.String(), "fix: roles.main") {
		t.Fatalf("slow\n%s", out.String())
	}

	out.Reset()
	env.Profile.SetMain("http://127.0.0.1:1/v1", env.Profile.MainModelID(), env.Profile.MainThinking())
	env.Profile.UI.ProbeTimeoutMS = 200
	if code := runDoctor(context.Background(), env, nil); code != 1 {
		t.Fatalf("down exit %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "unreachable") {
		t.Fatalf("down\n%s", out.String())
	}
}

func TestDoctorThinkingReject(t *testing.T) {
	restoreDoctorHooks(t)
	doctorLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"alpha"}]}`))
	}))
	defer srv.Close()
	env, out, home := doctorEnv(t, srv.URL+"/v1")
	doctorHome = func() (string, error) { return home, nil }
	env.Profile.SetMain(env.Profile.MainEndpoint(), "alpha", true)
	if code := runDoctor(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "!") || !strings.Contains(out.String(), "http 400") || !strings.Contains(out.String(), "thinking_kwargs") {
		t.Fatalf("%s", out.String())
	}
}

func TestDoctorMissingBinaryAndModes(t *testing.T) {
	restoreDoctorHooks(t)
	doctorLookPath = func(name string) (string, error) {
		if name == "chromium" || name == "git" || name == "chromium-browser" ||
			name == "google-chrome-stable" || name == "google-chrome" || name == "brave" || name == "brave-browser" {
			return "", exec.ErrNotFound
		}
		return "/usr/bin/" + name, nil
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"alpha"}]}`))
	}))
	defer srv.Close()
	env, out, home := doctorEnv(t, srv.URL+"/v1")
	if err := os.Chmod(env.ConfigPath, 0644); err != nil {
		t.Fatal(err)
	}
	secret := "pg-secret-value"
	if err := os.WriteFile(filepath.Join(home, ".pgpass"), []byte(secret+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	doctorHome = func() (string, error) { return home, nil }
	if code := runDoctor(context.Background(), env, nil); code != 1 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	text := out.String()
	for _, want := range []string{"0644", "chmod 0600", "not found", chromeFix, "install git", "chmod 0600 ~/.pgpass"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q\n%s", want, text)
		}
	}
	if strings.Contains(text, secret) {
		t.Fatal("pgpass secret leaked")
	}
	if strings.Contains(text, "mmdc") || strings.Contains(text, "mermaid-cli") {
		t.Fatalf("mmdc still checked\n%s", text)
	}
}

func TestDoctorConfigMissing(t *testing.T) {
	restoreDoctorHooks(t)
	doctorLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	env, out, home := doctorEnv(t, "http://127.0.0.1:9/v1")
	env.ConfigPath = filepath.Join(home, "missing.yaml")
	doctorHome = func() (string, error) { return home, nil }
	if code := runDoctor(context.Background(), env, nil); code != 1 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "missing") || !strings.Contains(out.String(), "fix: konsolai /config init") {
		t.Fatalf("%s", out.String())
	}
}

func TestDoctorBadConfig(t *testing.T) {
	restoreDoctorHooks(t)
	doctorLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	env, out, home := doctorEnv(t, "http://127.0.0.1:9/v1")
	if err := os.WriteFile(env.ConfigPath, []byte("profiles:\n  default:\n    llm:\n      endpoint: not-a-url\n"), 0600); err != nil {
		t.Fatal(err)
	}
	doctorHome = func() (string, error) { return home, nil }
	if code := runDoctor(context.Background(), env, nil); code != 1 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "fix: konsolai /config validate") {
		t.Fatalf("%s", out.String())
	}
}
