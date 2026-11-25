package turn

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SniksaX/Konsolai/internal/agent"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/memory"
)

// localFastServer answers a greeting for the local fast role.
func localFastServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "fast", "max_model_len": 8192}}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		delta, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": "hello from fast"}}}})
		_, _ = w.Write([]byte("data: " + string(delta) + "\n\ndata: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// remoteIDRunner has a remote main with an EMPTY model id and a local fast role.
func remoteIDRunner(t *testing.T, fast *httptest.Server) (*Runner, *bytes.Buffer, *atomic.Int32) {
	t.Helper()
	config.ResetRemoteState()
	t.Cleanup(config.ResetRemoteState)
	t.Cleanup(func() { config.SetRemotePrompt(nil); config.SetRemoteInteractive(nil) })
	config.SetRemoteInteractive(func() bool { return true })
	var prompts atomic.Int32
	config.SetRemotePrompt(func(string) (bool, error) { prompts.Add(1); return true, nil })

	c := config.Builtin()
	c.Models["remote-main"] = config.Model{Endpoint: "http://203.0.113.9/v1"}
	c.Models["local-fast"] = config.Model{Endpoint: fast.URL + "/v1", Model: "fast"}
	c.Roles.Main, c.Roles.Fast = "remote-main", "local-fast"
	c.Privacy.AllowRemote = true
	var out, errb bytes.Buffer
	r := &Runner{
		Profile:    c,
		Client:     llm.New("http://203.0.113.9/v1", ""),
		FastClient: llm.NewFast(fast.URL+"/v1", "fast"),
		Mem:        memory.NewGuard(nil, nil),
		Out:        &out,
		Err:        &errb,
		OpenConfirm: func(bool) (agent.Confirmer, func(), error) {
			return autoConfirm{}, func() {}, nil
		},
	}
	return r, &errb, &prompts
}

// TestRemoteEmptyIDFailsBeforeTheConfirmationPrompt: a remote model with no id
// cannot be resolved without asking the provider, so the turn fails with the
// /set model id line instead of prompting first.
func TestRemoteEmptyIDFailsBeforeTheConfirmationPrompt(t *testing.T) {
	fast := localFastServer(t)
	r, errb, prompts := remoteIDRunner(t, fast)
	res := r.RunTurn(context.Background(), Request{
		Text: "explain this stack trace error", Intent: "answer", Effort: "low",
		Yes: true, Cwd: t.TempDir(), New: true,
	})
	if res.ExitCode != 1 {
		t.Fatalf("exit %d: %s", res.ExitCode, errb.String())
	}
	if prompts.Load() != 0 {
		t.Fatalf("the remote confirmation was shown %d times before the id failure", prompts.Load())
	}
	text := errb.String()
	if !strings.Contains(text, "run: /set model id") || !strings.Contains(text, "remote-main") {
		t.Fatalf("stderr %q does not name the actionable command", text)
	}
}

// TestLocalGreetingIgnoresTheRemoteEmptyID: a greeting served by the local fast
// role must not fail or prompt because the unused remote main has no id.
func TestLocalGreetingIgnoresTheRemoteEmptyID(t *testing.T) {
	fast := localFastServer(t)
	r, errb, prompts := remoteIDRunner(t, fast)
	res := r.RunTurn(context.Background(), Request{
		Text: "hello", Intent: "answer", Effort: "none", Yes: true, Cwd: t.TempDir(), New: true,
	})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d: %s", res.ExitCode, errb.String())
	}
	if prompts.Load() != 0 {
		t.Fatalf("a local greeting prompted for the remote host")
	}
	if !strings.Contains(res.Answer, "hello from fast") {
		t.Fatalf("answer %q", res.Answer)
	}
}
