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

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
)

type remoteCounters struct {
	hits   atomic.Int32
	prompt atomic.Bool
	order  atomic.Bool
}

func countingModelServer(t *testing.T, c *remoteCounters) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "m", "max_model_len": 4096}}})
			return
		}
		c.hits.Add(1)
		if !c.prompt.Load() {
			c.order.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func remoteGreetingRunner(t *testing.T) (*Runner, *remoteCounters, *remoteCounters, *bytes.Buffer) {
	t.Helper()
	config.ResetRemoteState()
	t.Cleanup(config.ResetRemoteState)
	t.Cleanup(func() { config.SetRemotePrompt(nil); config.SetRemoteInteractive(nil) })
	config.SetRemoteInteractive(func() bool { return true })

	mainC, fastC := &remoteCounters{}, &remoteCounters{}
	mainSrv := countingModelServer(t, mainC)
	fastSrv := countingModelServer(t, fastC)

	c := config.Builtin()
	c.Models["remote-main"] = config.Model{Endpoint: "http://203.0.113.9/v1", Model: "m"}
	c.Models["local-fast"] = config.Model{Endpoint: fastSrv.URL + "/v1"}
	c.Roles.Main, c.Roles.Fast = "remote-main", "local-fast"
	c.Privacy.AllowRemote = true

	r := testRunner(t, llm.New(mainSrv.URL+"/v1", "m"), autoConfirm{})
	r.Profile = c
	r.FastClient = llm.NewFast(fastSrv.URL+"/v1", "m")
	var errB bytes.Buffer
	r.Err = &errB
	return r, mainC, fastC, &errB
}

func TestRemoteGreetingDoesNotPromptOrSend(t *testing.T) {
	r, mainC, fastC, errB := remoteGreetingRunner(t)
	var prompts atomic.Int32
	config.SetRemotePrompt(func(string) (bool, error) { prompts.Add(1); return true, nil })
	res := r.RunTurn(context.Background(), Request{Text: "hello", Intent: "answer", Cwd: t.TempDir(), New: true})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d: %s", res.ExitCode, errB.String())
	}
	if prompts.Load() != 0 {
		t.Fatalf("greeting prompted %d times", prompts.Load())
	}
	if mainC.hits.Load() != 0 {
		t.Fatalf("remote main received %d requests", mainC.hits.Load())
	}
	if fastC.hits.Load() == 0 {
		t.Fatal("local fast model received no request")
	}
}

func TestRemoteMainPromptsOnceBeforeTheRequest(t *testing.T) {
	r, mainC, _, errB := remoteGreetingRunner(t)
	var prompts atomic.Int32
	config.SetRemotePrompt(func(host string) (bool, error) {
		prompts.Add(1)
		if !strings.Contains(host, "203.0.113.9") {
			t.Errorf("prompt host %q", host)
		}
		mainC.prompt.Store(true)
		return true, nil
	})
	res := r.RunTurn(context.Background(), Request{Text: "explain this error in the log", Intent: "answer", Cwd: t.TempDir(), New: true})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d: %s", res.ExitCode, errB.String())
	}
	if prompts.Load() != 1 {
		t.Fatalf("prompts %d", prompts.Load())
	}
	if mainC.hits.Load() == 0 {
		t.Fatal("remote main was never called")
	}
	if mainC.order.Load() {
		t.Fatal("a request reached the remote host before the confirmation")
	}
}

func TestRemoteMainDeclineSendsNothing(t *testing.T) {
	r, mainC, _, errB := remoteGreetingRunner(t)
	var prompts atomic.Int32
	config.SetRemotePrompt(func(string) (bool, error) { prompts.Add(1); return false, nil })
	res := r.RunTurn(context.Background(), Request{Text: "explain this error in the log", Intent: "answer", Cwd: t.TempDir(), New: true})
	if res.ExitCode != 1 {
		t.Fatalf("exit %d", res.ExitCode)
	}
	if prompts.Load() != 1 {
		t.Fatalf("prompts %d", prompts.Load())
	}
	if mainC.hits.Load() != 0 {
		t.Fatalf("remote main received %d requests after decline", mainC.hits.Load())
	}
	if !strings.Contains(errB.String(), "declined") {
		t.Fatalf("stderr %q", errB.String())
	}
}
