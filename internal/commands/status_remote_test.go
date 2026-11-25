package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

// statusLine returns the stdout line that starts with prefix.
func statusLine(t *testing.T, out, prefix string) string {
	t.Helper()
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(ln, prefix) {
			return ln
		}
	}
	t.Fatalf("no %q line in\n%s", prefix, out)
	return ""
}

func TestStatusMarksRemoteEndpoint(t *testing.T) {
	env, out, _ := testEnv(t)
	env.Profile = config.WithMain("http://remote.invalid/v1", "m")
	if code := runStatus(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if line := statusLine(t, out.String(), "endpoint "); !strings.Contains(line, "remote.invalid  remote") {
		t.Fatalf("remote main not marked: %q", line)
	}
}

func TestStatusKeepsLocalEndpointUnmarked(t *testing.T) {
	env, out, _ := testEnv(t)
	if code := runStatus(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if line := statusLine(t, out.String(), "endpoint "); strings.Contains(line, "remote") {
		t.Fatalf("local main marked: %q", line)
	}
}

func TestStatusMarksRemoteFastEndpoint(t *testing.T) {
	env, out, _ := testEnv(t)
	env.Profile = config.WithMain("http://127.0.0.1:9/v1", "m")
	env.Profile.SetFast("fast", "http://remote.invalid/v1", "mini", false)
	if code := runStatus(context.Background(), env, nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if line := statusLine(t, out.String(), "fast_endpoint "); !strings.Contains(line, "remote.invalid  remote") {
		t.Fatalf("remote fast not marked: %q", line)
	}
	if line := statusLine(t, out.String(), "endpoint "); strings.Contains(line, "remote") {
		t.Fatalf("local main marked: %q", line)
	}
}
