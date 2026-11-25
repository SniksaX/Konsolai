package turn

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
)

func remoteAnswerServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "m", "max_model_len": 4096}}})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRemoteTokenCapTripsThroughRunTurn(t *testing.T) {
	config.ResetRemoteState()
	t.Cleanup(config.ResetRemoteState)
	srv := remoteAnswerServer(t)
	r := testRunner(t, llm.New(srv.URL+"/v1", "m"), autoConfirm{})
	c := config.Builtin()
	c.Models["api"] = config.Model{Endpoint: "http://203.0.113.9/v1", Model: "m"}
	c.Roles.Main = "api"
	c.Privacy.SessionTokenCap = 1
	r.Profile = c
	var errB bytes.Buffer
	r.Err = &errB

	res := r.RunTurn(context.Background(), Request{Text: "hi", Intent: "answer", Yes: true, Cwd: t.TempDir(), New: true})
	if res.ExitCode != 0 {
		t.Fatalf("first turn exit %d: %s", res.ExitCode, errB.String())
	}
	errB.Reset()
	res = r.RunTurn(context.Background(), Request{Text: "again", Intent: "answer", Yes: true, Cwd: t.TempDir(), New: true})
	if res.ExitCode != 1 || !strings.Contains(errB.String(), "privacy.session_token_cap") {
		t.Fatalf("cap turn exit %d stderr %q", res.ExitCode, errB.String())
	}
}
