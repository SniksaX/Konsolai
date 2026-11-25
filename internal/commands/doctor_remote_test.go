package commands

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

func doctorRemoteEnv(t *testing.T) (*Env, *strings.Builder) {
	t.Helper()
	restoreDoctorHooks(t)
	doctorLookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"local-model","max_model_len":8192}]}`))
	}))
	t.Cleanup(srv.Close)
	env, out, home := doctorEnv(t, srv.URL+"/v1")
	// config.ReadSecret reads from HOME, so keep the developer's secrets file
	// out of the test.
	t.Setenv("HOME", home)
	doctorHome = func() (string, error) { return home, nil }
	return env, out
}

func TestDoctorRemoteModelKeyAndHost(t *testing.T) {
	env, out := doctorRemoteEnv(t)
	keyEnv := "KONSOLAI_DOCTOR_REMOTE_KEY"
	key := "sk" + "-doctor-" + "remote-" + "0a1b2c3d4e5f"
	t.Setenv(keyEnv, key)
	t.Setenv("KONSOLAI_DOCTOR_BARE_KEY", "")

	env.Profile.Models["keyed"] = config.Model{Endpoint: "https://203.0.113.10/v1", Model: "k", APIKeyEnv: keyEnv}
	env.Profile.Models["bare"] = config.Model{Endpoint: "http://203.0.113.9/v1", Model: "b", APIKeyEnv: "KONSOLAI_DOCTOR_BARE_KEY"}
	if err := config.Validate(env.Profile); err != nil {
		t.Fatalf("profile no longer validates: %v", err)
	}

	if code := runDoctor(context.Background(), env, nil); code != 0 {
		t.Fatalf("doctor exit %d\n%s", code, out.String())
	}
	text := out.String()
	lines := strings.Split(text, "\n")

	keyed := lineAfter(lines, "remote keyed")
	if keyed == "" {
		t.Fatalf("no remote keyed line\n%s", text)
	}
	for _, want := range []string{"203.0.113.10", "key present"} {
		if !strings.Contains(keyed, want) {
			t.Fatalf("keyed line %q is missing %q", keyed, want)
		}
	}
	for _, unwanted := range []string{"plain http", "fix:"} {
		if strings.Contains(keyed, unwanted) {
			t.Fatalf("keyed line %q should not contain %q", keyed, unwanted)
		}
	}

	bare := lineAfter(lines, "remote bare")
	if bare == "" {
		t.Fatalf("no remote bare line\n%s", text)
	}
	for _, want := range []string{"203.0.113.9", "key absent", "plain http"} {
		if !strings.Contains(bare, want) {
			t.Fatalf("bare line %q is missing %q", bare, want)
		}
	}
	fix := lineAfter(lines, "fix:")
	if !strings.Contains(fix, "/set key bare") || !strings.Contains(fix, "KONSOLAI_DOCTOR_BARE_KEY") || !strings.Contains(fix, "models.bare.endpoint") {
		t.Fatalf("bare fix %q does not name the key and the https change", fix)
	}
	if strings.Contains(text, key) {
		t.Fatal("the doctor output leaked the remote key")
	}
	if strings.Contains(text, "remote default") {
		t.Fatalf("a loopback model was reported as remote\n%s", text)
	}
}

// lineAfter returns the first output line that contains want, or an empty
// string when no line does. It also checks the following line for a fix.
func lineAfter(lines []string, want string) string {
	for i, line := range lines {
		if !strings.Contains(line, want) {
			continue
		}
		if i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "fix:") {
			return line + "\n" + lines[i+1]
		}
		return line
	}
	return ""
}
