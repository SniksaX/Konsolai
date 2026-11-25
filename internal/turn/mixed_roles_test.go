package turn

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/embed"
	"github.com/SniksaX/Konsolai/internal/llm"
)

// mixedRoleServer is one fake OpenAI-compatible endpoint that counts the
// requests each role makes. It answers /models and streams one answer.
type mixedRoleServer struct {
	srv        *httptest.Server
	modelCalls atomic.Int32
	chatCalls  atomic.Int32
}

func newMixedRoleServer(t *testing.T, answer string) *mixedRoleServer {
	t.Helper()
	m := &mixedRoleServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		m.modelCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "m", "max_model_len": 8192}},
		})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		m.chatCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		payload, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"delta": map[string]any{"content": answer}}},
			"usage":   map[string]int{"prompt_tokens": 4, "completion_tokens": 2},
		})
		_, _ = io.WriteString(w, "data: "+string(payload)+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

// localEmbedServer is a loopback embedding endpoint; a turn does not call it,
// it only proves that a local embed URL is accepted next to a remote chat model.
func localEmbedServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/embed") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.2]]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// mixedRoleProfile is roles.main on a public test address, roles.fast and
// roles.embed on loopback.
func mixedRoleProfile(t *testing.T, fastURL, embedURL string) config.Config {
	t.Helper()
	c := config.Builtin()
	c.Models["cloud"] = config.Model{Endpoint: "http://203.0.113.9/v1", Model: "m"}
	c.Models["fast"] = config.Model{Endpoint: fastURL + "/v1", Model: "f"}
	c.Roles.Main = "cloud"
	c.Roles.Fast = "fast"
	c.Roles.Embed = embedURL
	c.Privacy.AllowRemote = true
	c.Privacy.RemoteRoles = []string{"main", "fast"}
	if err := config.Validate(c); err != nil {
		t.Fatalf("mixed roles should validate: %v", err)
	}
	return c
}

func mixedRoleRunner(t *testing.T, c config.Config, mainSrv, fastSrv *mixedRoleServer) (*Runner, *bytes.Buffer) {
	t.Helper()
	r := testRunner(t, llm.New(mainSrv.srv.URL+"/v1", "m"), autoConfirm{})
	r.Profile = c
	r.FastClient = llm.NewFast(fastSrv.srv.URL+"/v1", "f")
	r.Embed = embed.New(c.Roles.Embed, "")
	var errB bytes.Buffer
	r.Err = &errB
	return r, &errB
}

func TestMixedRolesRemoteMainCallWorks(t *testing.T) {
	config.ResetRemoteState()
	t.Cleanup(config.ResetRemoteState)
	prompts := 0
	config.SetRemotePrompt(func(string) (bool, error) {
		prompts++
		return true, nil
	})
	t.Cleanup(func() { config.SetRemotePrompt(nil) })

	mainSrv := newMixedRoleServer(t, "ok from main")
	fastSrv := newMixedRoleServer(t, "local fast")
	embedSrv := localEmbedServer(t)
	c := mixedRoleProfile(t, fastSrv.srv.URL, embedSrv.URL)
	r, errB := mixedRoleRunner(t, c, mainSrv, fastSrv)

	// The forced intent keeps low effort, so the answer streams from main while
	// the fast role is only probed on loopback.
	res := r.RunTurn(context.Background(), Request{
		Text: "say ok", Intent: "answer", Yes: true, Cwd: t.TempDir(), New: true,
	})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d stderr %q", res.ExitCode, errB.String())
	}
	if !strings.Contains(res.Answer, "ok from main") {
		t.Fatalf("answer %q did not come from main", res.Answer)
	}
	if mainSrv.chatCalls.Load() < 1 {
		t.Fatal("the remote main endpoint got no chat call")
	}
	if fastSrv.modelCalls.Load() < 1 {
		t.Fatal("the local fast endpoint was not probed")
	}
	if fastSrv.chatCalls.Load() != 0 {
		t.Fatalf("the low-effort answer wrongly used fast: %d chat calls", fastSrv.chatCalls.Load())
	}
	if prompts != 0 {
		t.Fatalf("--yes asked for a confirmation %d times", prompts)
	}
	if c.ModelRemote("fast") {
		t.Fatal("the fast role is not local")
	}
	if c.ModelRemote("cloud") != true || !c.Privacy.AllowRemote || !c.RemoteRoleAllowed("main") {
		t.Fatalf("main gating state %+v", c.Privacy)
	}
}

func TestMixedRolesRemoteMainBlockedBeforeAnyRequest(t *testing.T) {
	config.ResetRemoteState()
	t.Cleanup(config.ResetRemoteState)

	mainSrv := newMixedRoleServer(t, "never")
	fastSrv := newMixedRoleServer(t, "never")
	embedSrv := localEmbedServer(t)
	c := mixedRoleProfile(t, fastSrv.srv.URL, embedSrv.URL)
	c.Privacy.AllowRemote = false
	r, errB := mixedRoleRunner(t, c, mainSrv, fastSrv)

	res := r.RunTurn(context.Background(), Request{
		Text: "say ok", Intent: "answer", Yes: true, Cwd: t.TempDir(), New: true,
	})
	if res.ExitCode != 1 {
		t.Fatalf("exit %d stderr %q", res.ExitCode, errB.String())
	}
	for _, want := range []string{"privacy.allow_remote", `"cloud"`, "203.0.113.9"} {
		if !strings.Contains(errB.String(), want) {
			t.Fatalf("stderr %q is missing %q", errB.String(), want)
		}
	}
	if mainSrv.modelCalls.Load() != 0 || mainSrv.chatCalls.Load() != 0 {
		t.Fatalf("the blocked main endpoint saw requests: %+v", mainSrv)
	}
	if fastSrv.modelCalls.Load() != 0 || fastSrv.chatCalls.Load() != 0 {
		t.Fatal("the local fast endpoint was contacted before the gate")
	}
	// The local roles keep no gate of their own.
	if err := c.RemoteCallPolicy("fast", "fast", false)(); err != nil {
		t.Fatalf("the local fast model was gated: %v", err)
	}
}

func TestMixedRolesRemoteRoleListBlocksMain(t *testing.T) {
	config.ResetRemoteState()
	t.Cleanup(config.ResetRemoteState)

	mainSrv := newMixedRoleServer(t, "never")
	fastSrv := newMixedRoleServer(t, "never")
	embedSrv := localEmbedServer(t)
	c := mixedRoleProfile(t, fastSrv.srv.URL, embedSrv.URL)
	c.Privacy.RemoteRoles = []string{"fast"}
	r, errB := mixedRoleRunner(t, c, mainSrv, fastSrv)

	res := r.RunTurn(context.Background(), Request{
		Text: "say ok", Intent: "answer", Yes: true, Cwd: t.TempDir(), New: true,
	})
	if res.ExitCode != 1 {
		t.Fatalf("exit %d stderr %q", res.ExitCode, errB.String())
	}
	for _, want := range []string{"privacy.remote_roles", `"main"`, `"cloud"`} {
		if !strings.Contains(errB.String(), want) {
			t.Fatalf("stderr %q is missing %q", errB.String(), want)
		}
	}
	if mainSrv.modelCalls.Load() != 0 || mainSrv.chatCalls.Load() != 0 {
		t.Fatal("the unlisted role reached the remote endpoint")
	}

	// Listing main instead lets the same turn run from main.
	config.ResetRemoteState()
	c.Privacy.RemoteRoles = []string{"main"}
	r, errB = mixedRoleRunner(t, c, mainSrv, fastSrv)
	res = r.RunTurn(context.Background(), Request{
		Text: "say ok", Intent: "answer", Yes: true, Cwd: t.TempDir(), New: true,
	})
	if res.ExitCode != 0 {
		t.Fatalf("listed main exit %d stderr %q", res.ExitCode, errB.String())
	}
	if mainSrv.chatCalls.Load() < 1 {
		t.Fatal("the listed main role did not reach the remote endpoint")
	}
}
