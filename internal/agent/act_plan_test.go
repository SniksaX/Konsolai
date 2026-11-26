package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/tools"
	"github.com/SniksaX/Konsolai/internal/ui"
)

type offerRecorder struct {
	mu      sync.Mutex
	seen    [][]string
	bodies  []string
	replies []string
	idx     int
}

func (o *offerRecorder) client(t *testing.T) *llm.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"m","max_model_len":16384}]}`))
			return
		}
		var body struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				}
			} `json:"tools"`
			Messages []map[string]any `json:"messages"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		names := make([]string, 0, len(body.Tools))
		for _, tl := range body.Tools {
			names = append(names, tl.Function.Name)
		}
		o.mu.Lock()
		o.seen = append(o.seen, names)
		o.bodies = append(o.bodies, string(raw))
		i := o.idx
		if o.idx < len(o.replies)-1 {
			o.idx++
		}
		o.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, o.replies[i])
	}))
	t.Cleanup(srv.Close)
	c := llm.New(srv.URL+"/v1", "m")
	c.PinContext(16384)
	c.SetRetry(0, 0)
	return c
}

func (o *offerRecorder) firstTools() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.seen) == 0 {
		return nil
	}
	return o.seen[0]
}

func (o *offerRecorder) has(name string) bool {
	for _, n := range o.firstTools() {
		if n == name {
			return true
		}
	}
	return false
}

func actRun(t *testing.T, rec *offerRecorder, requirePlan bool) string {
	t.Helper()
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	err = Run(context.Background(), Config{
		Client: rec.client(t), Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve}, Out: &out, Err: io.Discard,
		RequirePlan: requirePlan, MaxSteps: 6, Intent: "act",
	}, "sys", "create a file hello.txt containing hi")
	return out.String()
}

func chatText(text string) string {
	b, _ := json.Marshal(text)
	return `{"choices":[{"message":{"role":"assistant","content":` + string(b) + `}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`
}

func chatCall(id, name, args string) string {
	a, _ := json.Marshal(args)
	return `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"` + id + `","type":"function","function":{"name":"` + name + `","arguments":` + string(a) + `}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`
}

// TestActLowOfferIncludesMutatingTools: when the plan is not required, write
// and shell tools are offered immediately (local-model behaviour unchanged).
func TestActLowOfferIncludesMutatingTools(t *testing.T) {
	rec := &offerRecorder{replies: []string{chatCall("1", "finish", `{"summary":"done"}`)}}
	_ = actRun(t, rec, false)
	for _, want := range []string{"write_file", "run_shell"} {
		if !rec.has(want) {
			t.Fatalf("act without a required plan must offer %s, got %v", want, rec.firstTools())
		}
	}
}

// TestTextOnlyReplyUnderRequiredPlanIsNudgedOnce: a change request whose model
// answers with prose must be nudged to call propose_plan exactly once.
func TestTextOnlyReplyUnderRequiredPlanIsNudgedOnce(t *testing.T) {
	rec := &offerRecorder{replies: []string{
		chatText("I will create hello.txt now."),
		chatCall("2", "propose_plan", `{"summary":"create hello.txt","steps":["write the file"],"files":["hello.txt"],"commands":["touch hello.txt"]}`),
		chatCall("3", "finish", `{"summary":"done"}`),
	}}
	out := actRun(t, rec, true)
	rec.mu.Lock()
	requests := len(rec.bodies)
	body2 := ""
	if requests > 1 {
		body2 = rec.bodies[1]
	}
	rec.mu.Unlock()
	if requests < 3 {
		t.Fatalf("text-only reply was not nudged to propose_plan: %d requests\n%s", requests, out)
	}
	if !strings.Contains(body2, "call propose_plan") {
		t.Fatalf("the retry request misses the nudge text:\n%s", body2)
	}
	if !strings.Contains(out, "plan:") {
		t.Fatalf("the plan was never approved after the nudge:\n%s", out)
	}
	// The mutating tools unlock only after approval.
	rec.mu.Lock()
	t.Logf("seen=%v", rec.seen)
	lastTools := rec.seen[len(rec.seen)-1]
	rec.mu.Unlock()
	foundWrite := false
	for _, n := range lastTools {
		if n == "write_file" {
			foundWrite = true
		}
	}
	if !foundWrite {
		t.Fatalf("write_file was not unlocked after approval: %v", lastTools)
	}
}

// TestSecondTextReplyUnderRequiredPlanStops: only one nudge is sent.
func TestSecondTextReplyUnderRequiredPlanStops(t *testing.T) {
	rec := &offerRecorder{replies: []string{chatText("not now"), chatText("still not now")}}
	_ = actRun(t, rec, true)
	rec.mu.Lock()
	requests := len(rec.bodies)
	second := ""
	if requests > 1 {
		second = rec.bodies[1]
	}
	rec.mu.Unlock()
	if requests != 2 {
		t.Fatalf("want exactly one retry, got %d requests", requests)
	}
	if !strings.Contains(second, "call propose_plan") {
		t.Fatalf("the single retry misses the nudge:\n%s", second)
	}
}
