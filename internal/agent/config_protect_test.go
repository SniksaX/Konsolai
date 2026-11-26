package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/tools"
	"github.com/SniksaX/Konsolai/internal/ui"
)

func protectProject(t *testing.T) (project string, reg *tools.Registry) {
	t.Helper()
	root := t.TempDir()
	project = filepath.Join(root, "project")
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(filepath.Join(project, ".real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "konsolai"), 0o700); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(project, ".real", "config.yaml")
	body := "defaults:\n  database:\n    url: postgres://u:p@db.internal.example:5432/k\n  mcp:\n    servers:\n      s:\n        command: server-binary\n        args: [SECRETARG]\n        env:\n          TOKEN: SECRETENV\n        url: https://mcp.internal.example/rpc\n"
	if err := os.WriteFile(active, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(active, filepath.Join(project, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("KONSOLAI_CONFIG", active)
	g, err := safety.New(project)
	if err != nil {
		t.Fatal(err)
	}
	return project, tools.NewRegistry(g, io.Discard, nil, nil, nil)
}

func fakeCapture(t *testing.T, replies []string) (*llm.Client, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	idx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"m","max_model_len":16384}]}`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		i := idx
		if idx < len(replies)-1 {
			idx++
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, replies[i])
	}))
	t.Cleanup(srv.Close)
	c := llm.New(srv.URL+"/v1", "m")
	c.PinContext(16384)
	c.SetRetry(0, 0)
	return c, &bodies
}

// TestAgentReadOfConfigIsMasked proves no raw config content reaches a remote
// provider's request bodies.
func TestAgentReadOfConfigIsMasked(t *testing.T) {
	_, reg := protectProject(t)
	replies := []string{
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"config.yaml\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"2","type":"function","function":{"name":"finish","arguments":"{\"summary\":\"done\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
	}
	client, bodies := fakeCapture(t, replies)
	if err := Run(context.Background(), Config{
		Client: client, Tools: reg, Confirmer: stubConfirm{d: ui.Approve},
		Out: io.Discard, Err: io.Discard, MaxSteps: 4, Intent: "act",
	}, "sys", "read the config"); err != nil {
		t.Fatal(err)
	}
	for _, body := range *bodies {
		for _, leak := range []string{"server-binary", "SECRETARG", "SECRETENV", "db.internal.example", "mcp.internal.example"} {
			if strings.Contains(body, leak) {
				t.Fatalf("raw config content %q reached the provider body:\n%s", leak, body)
			}
		}
	}
	joined := strings.Join(*bodies, "\n")
	// JSON escapes < and >, so accept the escaped marker as well.
	if !strings.Contains(joined, "<set>") && !strings.Contains(joined, `\u003cset\u003e`) &&
		!strings.Contains(joined, "<masked>") && !strings.Contains(joined, `\u003cmasked\u003e`) {
		t.Fatal("the masked config view never reached the model")
	}
}

// TestAgentCannotEditConfigThroughPlan: a plan route that names a shell edit of
// the config is refused with the /set pointer.
func TestAgentCannotEditConfigThroughPlan(t *testing.T) {
	_, reg := protectProject(t)
	replies := []string{
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"1","type":"function","function":{"name":"propose_plan","arguments":"{\"summary\":\"edit config\",\"steps\":[\"sed -i s/a/b/ config.yaml\"],\"commands\":[\"sed -i s/a/b/ config.yaml\"]}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"2","type":"function","function":{"name":"run_shell","arguments":"{\"cmd\":\"sed -i s/a/b/ config.yaml\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
		`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"3","type":"function","function":{"name":"finish","arguments":"{\"summary\":\"done\"}"}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
	}
	client, _ := fakeCapture(t, replies)
	var out strings.Builder
	_ = Run(context.Background(), Config{
		Client: client, Tools: reg, Confirmer: stubConfirm{d: ui.Approve},
		Out: &out, Err: io.Discard, RequirePlan: true, MaxSteps: 4, Intent: "build",
	}, "sys", "edit the config")
	if !strings.Contains(out.String(), "changed only through /set") {
		t.Fatalf("config edit through the plan route was not refused:\n%s", out.String())
	}
}

var _ = json.Marshal
