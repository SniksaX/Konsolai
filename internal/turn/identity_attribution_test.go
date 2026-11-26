package turn

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SniksaX/Konsolai/internal/agent"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/intent"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/memory"
	"github.com/SniksaX/Konsolai/internal/sysctx"
)

// roleServer is one fake OpenAI-compatible role. It records every chat body so
// a test can read the system context the model really received.
type roleServer struct {
	srv    *httptest.Server
	id     string
	answer string
	// toolMsg, when set, is returned as a non-streaming assistant message with
	// tool calls instead of the streamed answer.
	toolMsg *llm.Message

	chats  atomic.Int32
	probes atomic.Int32
	mu     sync.Mutex
	bodies []string
}

func newRoleServer(t *testing.T, id, answer string) *roleServer {
	t.Helper()
	rs := &roleServer{id: id, answer: answer}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		rs.probes.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": rs.id, "max_model_len": 8192}},
		})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		rs.chats.Add(1)
		body, _ := io.ReadAll(r.Body)
		rs.mu.Lock()
		rs.bodies = append(rs.bodies, string(body))
		rs.mu.Unlock()
		if rs.toolMsg != nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": rs.toolMsg}},
				"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 2},
			})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		delta, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"delta": map[string]any{"content": rs.answer}}},
		})
		usage, _ := json.Marshal(map[string]any{
			"choices": []any{},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 2},
		})
		_, _ = io.WriteString(w, "data: "+string(delta)+"\n\n")
		_, _ = io.WriteString(w, "data: "+string(usage)+"\n\ndata: [DONE]\n\n")
	})
	rs.srv = httptest.NewServer(mux)
	t.Cleanup(rs.srv.Close)
	return rs
}

func (rs *roleServer) systemPrompts(t *testing.T) []string {
	t.Helper()
	rs.mu.Lock()
	defer rs.mu.Unlock()
	var out []string
	for _, body := range rs.bodies {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("decode chat body: %v", err)
		}
		if len(req.Messages) == 0 || req.Messages[0].Role != "system" {
			t.Fatalf("the chat body has no system message: %s", body)
		}
		out = append(out, req.Messages[0].Content)
	}
	return out
}

// modelRunner builds a Runner whose main role is named deepseek-flash with id
// main-model and whose fast role is named fast with id fast-model.
func modelRunner(t *testing.T, main, fast *roleServer) (*Runner, *strings.Builder, *strings.Builder) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	prof := config.Builtin()
	prof.Models = map[string]config.Model{
		"deepseek-flash": {Endpoint: main.srv.URL + "/v1", Model: main.id},
	}
	prof.Roles.Main = "deepseek-flash"
	if fast != nil {
		prof.Models["fast"] = config.Model{Endpoint: fast.srv.URL + "/v1", Model: fast.id}
		prof.Roles.Fast = "fast"
	}
	var out, errb strings.Builder
	return &Runner{
		Profile: prof,
		Mem:     memory.NewGuard(nil, nil),
		Out:     &out,
		Err:     &errb,
		OpenConfirm: func(bool) (agent.Confirmer, func(), error) {
			return autoConfirm{}, func() {}, nil
		},
	}, &out, &errb
}

func TestIdentityQuestionAnswersFromFactsWithoutAModel(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want []string
	}{
		{"english", "what model are you ?", []string{"I'm Konsolai", "main role:", "configured model facts"}},
		{"which", "which model is this", []string{"main role:"}},
		{"french", "quel modèle es-tu ?", []string{"Je suis Konsolai", "rôle main", "modèle"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main := newRoleServer(t, "main-model", "never")
			fast := newRoleServer(t, "fast-model", "never")
			r, _, errb := modelRunner(t, main, fast)
			res := r.RunTurn(context.Background(), Request{
				Text: tc.text, Yes: true, Cwd: t.TempDir(), New: true, Trace: true,
			})
			if res.ExitCode != 0 {
				t.Fatalf("exit %d: %s", res.ExitCode, errb.String())
			}
			if chats := main.chats.Load() + fast.chats.Load(); chats != 0 {
				t.Fatalf("the identity floor made %d model calls", chats)
			}
			if probes := main.probes.Load() + fast.probes.Load(); probes != 0 {
				t.Fatalf("the identity floor probed a model %d times", probes)
			}
			for _, want := range []string{
				"deepseek-flash", "main-model", main.srv.URL[len("http://"):],
				"fast", "fast-model", fast.srv.URL[len("http://"):],
			} {
				if !strings.Contains(res.Answer, want) {
					t.Fatalf("answer misses %q:\n%s", want, res.Answer)
				}
			}
			for _, want := range tc.want {
				if !strings.Contains(res.Answer, want) {
					t.Fatalf("answer misses %q:\n%s", want, res.Answer)
				}
			}
			if !strings.Contains(errb.String(), "answered from the configured model facts (no model call)") {
				t.Fatalf("trace misses the floor line: %q", errb.String())
			}
			if r.Serving.Role != "local" || r.Serving.Name != "facts" {
				t.Fatalf("serving attribution %+v", r.Serving)
			}
		})
	}
}

func TestIdentityFloorDetection(t *testing.T) {
	yes := []string{
		"what model are you", "what model are you?", "Which model is this?",
		"what model am I using", "what LLM are you", "who are you",
		"quel modèle es-tu", "C'est quel modèle ?", "tu es quel modèle",
	}
	for _, text := range yes {
		if !identityFloor(text) {
			t.Errorf("%q should floor", text)
		}
	}
	no := []string{
		"how do I set the model", "what model should I use for coding",
		"write a model file", "explain this code", "quelle heure est-il",
	}
	for _, text := range no {
		if identityFloor(text) {
			t.Errorf("%q should not floor", text)
		}
	}
}

// TestSystemContextCarriesTheModelFacts proves real ids reach the model on the
// answer and the lookup path.
func TestSystemContextCarriesTheModelFacts(t *testing.T) {
	main := newRoleServer(t, "main-model", "hello from main")
	fast := newRoleServer(t, "fast-model", "hello from fast")
	r, _, _ := modelRunner(t, main, fast)
	mainHost := main.srv.URL[len("http://"):]
	fastHost := fast.srv.URL[len("http://"):]

	res := r.RunTurn(context.Background(), Request{
		Text: "say hello", Intent: "answer", Effort: "low", Yes: true, Cwd: t.TempDir(), New: true,
	})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d", res.ExitCode)
	}
	prompts := main.systemPrompts(t)
	if len(prompts) == 0 {
		t.Fatal("the fake model received no prompt")
	}
	for _, want := range []string{
		"deepseek-flash", "main-model", mainHost,
		"fast", "fast-model", fastHost,
		"answered by the main role",
		`say "I don't know"`,
	} {
		if !strings.Contains(prompts[0], want) {
			t.Fatalf("the answer context misses %q:\n%s", want, prompts[0])
		}
	}

	lookup := newRoleServer(t, "lookup-model", "")
	lookup.toolMsg = &llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
		ID: "1", Type: "function", Function: llm.FunctionCall{Name: "finish", Arguments: `{"summary":"looked up"}`},
	}}}
	rl, _, _ := modelRunner(t, lookup, nil)
	res = rl.RunTurn(context.Background(), Request{
		Text: "what is in this folder", Intent: "lookup", Yes: true, Cwd: t.TempDir(), New: true,
	})
	if res.ExitCode != 0 {
		t.Fatalf("lookup exit %d", res.ExitCode)
	}
	for _, prompt := range lookup.systemPrompts(t) {
		if !strings.Contains(prompt, "deepseek-flash") || !strings.Contains(prompt, "lookup-model") {
			t.Fatalf("the lookup context misses the model facts:\n%s", prompt)
		}
	}
}

// TestFastAnswerRuleFollowsTheThreshold covers router.answer_main_above_tokens:
// fast answers only greetings and effort-none turns under the token threshold.
func TestFastAnswerRuleFollowsTheThreshold(t *testing.T) {
	long := strings.Repeat("word ", 400)
	cases := []struct {
		name      string
		text      string
		effort    string
		threshold int
		wantFast  bool
		skipLine  bool
	}{
		{"greeting", "hello", "none", 200, true, false},
		{"short effort none", "summarize the situation", "none", 5000, true, false},
		{"long effort none", long, "none", 200, false, true},
		{"effort low never fast", "summarize the situation", "low", 200, false, false},
		{"zero disables the gate", long, "none", 0, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			llm.ResetReachCache()
			t.Cleanup(llm.ResetReachCache)
			main := newRoleServer(t, "main-model", "from main")
			fast := newRoleServer(t, "fast-model", "from fast")
			r, _, errb := modelRunner(t, main, fast)
			r.Profile.Router.AnswerMainAboveTokens = tc.threshold
			res := r.RunTurn(context.Background(), Request{
				Text: tc.text, Intent: "answer", Effort: tc.effort, Yes: true,
				Cwd: t.TempDir(), New: true, Trace: true,
			})
			if res.ExitCode != 0 {
				t.Fatalf("exit %d: %s", res.ExitCode, errb.String())
			}
			if tc.wantFast {
				if fast.chats.Load() == 0 || main.chats.Load() != 0 {
					t.Fatalf("want fast (fast=%d main=%d)", fast.chats.Load(), main.chats.Load())
				}
				if !strings.Contains(res.Answer, "from fast") {
					t.Fatalf("answer did not come from fast: %q", res.Answer)
				}
				if r.Serving.Role != "fast" || r.Serving.ID != "fast-model" {
					t.Fatalf("serving attribution %+v", r.Serving)
				}
				if !strings.Contains(errb.String(), "model fast-model (fast)") {
					t.Fatalf("trace does not name the fast model: %q", errb.String())
				}
			} else {
				if fast.chats.Load() != 0 || main.chats.Load() == 0 {
					t.Fatalf("want main (fast=%d main=%d)", fast.chats.Load(), main.chats.Load())
				}
				if !strings.Contains(res.Answer, "from main") {
					t.Fatalf("answer did not come from main: %q", res.Answer)
				}
				if r.Serving.Role != "main" || r.Serving.ID != "main-model" {
					t.Fatalf("serving attribution %+v", r.Serving)
				}
				if !strings.Contains(errb.String(), "model main-model (main)") {
					t.Fatalf("trace does not name the main model: %q", errb.String())
				}
			}
			if tc.skipLine && !strings.Contains(errb.String(), "router.answer_main_above_tokens") {
				t.Fatalf("trace misses the threshold line: %q", errb.String())
			}
		})
	}
}

// TestEndOfTurnStatsNameTheServingModel checks the ui.stats line carries the
// serving model and its role.
func TestEndOfTurnStatsNameTheServingModel(t *testing.T) {
	main := newRoleServer(t, "main-model", "from main")
	fast := newRoleServer(t, "fast-model", "from fast")
	r, _, errb := modelRunner(t, main, fast)
	res := r.RunTurn(context.Background(), Request{
		Text: "hello", Intent: "answer", Effort: "none", Yes: true,
		Cwd: t.TempDir(), New: true,
	})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d", res.ExitCode)
	}
	if !strings.Contains(errb.String(), "model fast-model (fast)") {
		t.Fatalf("the end-of-turn stats line misses the serving model: %q", errb.String())
	}
}

func TestServingModelMarksRemoteEndpoints(t *testing.T) {
	prof := config.Builtin()
	prof.Models = map[string]config.Model{
		"cloud": {Endpoint: "http://203.0.113.9/v1", Model: "remote-model", PriceIn: 1},
		"fast":  {Endpoint: "http://127.0.0.1:9/v1", Model: "fast-model"},
	}
	prof.Roles.Main = "cloud"
	prof.Roles.Fast = "fast"
	r := &Runner{Profile: prof}
	main := r.servingModel(context.Background(), nil, "main")
	if main.Name != "cloud" || main.ID != "remote-model" || main.Role != "main" || !main.Remote {
		t.Fatalf("main attribution %+v", main)
	}
	if !strings.Contains(main.Label(), "(main) remote") {
		t.Fatalf("label %q", main.Label())
	}
	fast := r.servingModel(context.Background(), nil, "fast")
	if fast.Name != "fast" || fast.ID != "fast-model" || fast.Role != "fast" || fast.Remote {
		t.Fatalf("fast attribution %+v", fast)
	}
	if strings.Contains(fast.Label(), "remote") {
		t.Fatalf("fast label carries a remote marker: %q", fast.Label())
	}
	local := r.servingModel(context.Background(), nil, "local")
	if local.Name != "facts" || local.Role != "local" || local.Remote {
		t.Fatalf("local attribution %+v", local)
	}
}

func TestModelFactsUseTheConfiguredNamesAndIds(t *testing.T) {
	main := newRoleServer(t, "main-model", "x")
	fast := newRoleServer(t, "fast-model", "y")
	r, _, _ := modelRunner(t, main, fast)
	facts := r.modelFacts("fast")
	if facts.MainName != "deepseek-flash" || facts.MainID != "main-model" {
		t.Fatalf("main facts %+v", facts)
	}
	if !facts.HasFast || facts.FastName != "fast" || facts.FastID != "fast-model" {
		t.Fatalf("fast facts %+v", facts)
	}
	if facts.Role != "fast" {
		t.Fatalf("role %q", facts.Role)
	}
	text := facts.String()
	for _, want := range []string{"deepseek-flash", "main-model", "fast", "fast-model", "answered by the fast role", "local"} {
		if !strings.Contains(text, want) {
			t.Fatalf("facts miss %q:\n%s", want, text)
		}
	}
}

// TestDecideIntentFloorAppliesTheIdentityFloor proves the floor is part of the
// router decision, not only a RunTurn shortcut.
func TestDecideIntentFloorAppliesTheIdentityFloor(t *testing.T) {
	dec := DecideIntentFloor(context.Background(), nil, "", "what model are you ?", true, nil, false, nil, nil, "", nil)
	if dec.Intent != intent.Answer || dec.Reason != "identity floor" || dec.Effort != intent.EffortLow {
		t.Fatalf("floor decision %+v", dec)
	}
	if dec.Language != "en" {
		t.Fatalf("floor language %q", dec.Language)
	}
}

// TestBuildContextCarriesTheModelFacts proves the build intent's code context
// also carries the facts.
func TestBuildContextCarriesTheModelFacts(t *testing.T) {
	main := newRoleServer(t, "main-model", "x")
	r, _, _ := modelRunner(t, main, nil)
	snap := sysctx.Snapshot{OS: "Linux", Shell: "bash", CWD: "/tmp/proj"}
	snap.Model = r.modelFacts("main")
	got := codeContext(context.Background(), snap, t.TempDir())
	for _, want := range []string{"deepseek-flash", "main-model", "answered by the main role"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the build context misses %q:\n%s", want, got)
		}
	}
}

// TestIdentityAnswerNamesAnEmptyID: an unset model id reads as "id not set",
// never as if the server default had been chosen.
func TestIdentityAnswerNamesAnEmptyID(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	prof := config.Builtin()
	prof.Models = map[string]config.Model{"home": {Endpoint: "http://127.0.0.1:9/v1"}}
	prof.Roles.Main = "home"
	var out, errb strings.Builder
	r := &Runner{
		Profile: prof, Mem: memory.NewGuard(nil, nil), Out: &out, Err: &errb,
		OpenConfirm: func(bool) (agent.Confirmer, func(), error) {
			return autoConfirm{}, func() {}, nil
		},
	}
	res := r.RunTurn(context.Background(), Request{Text: "what model are you ?", Cwd: t.TempDir(), New: true})
	if res.ExitCode != 0 {
		t.Fatalf("exit %d: %s", res.ExitCode, errb.String())
	}
	if !strings.Contains(res.Answer, "id not set") {
		t.Fatalf("answer misses id not set:\n%s", res.Answer)
	}
	if strings.Contains(res.Answer, "server default") {
		t.Fatalf("answer still claims a server default:\n%s", res.Answer)
	}
}
