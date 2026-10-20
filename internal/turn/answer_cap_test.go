package turn

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SniksaX/Konsolai/internal/agent"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/intent"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/sysctx"
	"github.com/SniksaX/Konsolai/internal/ui"
)

func TestAnswerEffortCaps(t *testing.T) {
	low, _, ok := config.MatchKey("answer.max_tokens.low")
	if !ok || low.Path != "llm.max_tokens.answer_low" || low.Int != 600 {
		t.Fatalf("low key %+v ok=%v", low, ok)
	}
	high, _, ok := config.MatchKey("answer.max_tokens.high")
	if !ok || high.Path != "llm.max_tokens.answer_high" || high.Int != 2400 {
		t.Fatalf("high key %+v ok=%v", high, ok)
	}
	b := config.Builtin()
	if b.LLM.MaxTokens.AnswerLow != 600 || b.LLM.MaxTokens.AnswerHigh != 2400 {
		t.Fatalf("builtin low %d high %d", b.LLM.MaxTokens.AnswerLow, b.LLM.MaxTokens.AnswerHigh)
	}
	if b.AnswerEffortCap("none") != 0 || b.AnswerEffortCap("low") != 600 || b.AnswerEffortCap("high") != 2400 {
		t.Fatalf("caps none %d low %d high %d", b.AnswerEffortCap("none"), b.AnswerEffortCap("low"), b.AnswerEffortCap("high"))
	}

	cases := []struct {
		effort intent.Effort
		want   int
	}{
		{intent.EffortLow, 600},
		{intent.EffortHigh, 2400},
		{intent.EffortNone, 4096},
	}
	for _, tc := range cases {
		got := answerMaxTokens(t, tc.effort)
		if got != tc.want {
			t.Fatalf("effort %s max_tokens %d want %d", tc.effort, got, tc.want)
		}
	}
}

func answerMaxTokens(t *testing.T, effort intent.Effort) int {
	t.Helper()
	var maxTok int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "test", "max_model_len": 32768}},
			})
			return
		}
		var body struct {
			MaxTokens int `json:"max_tokens"`
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode %v", err)
		}
		maxTok = body.MaxTokens
		w.Header().Set("Content-Type", "text/event-stream")
		payload, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"delta": map[string]any{"content": "ok\n"}}},
		})
		_, _ = io.WriteString(w, "data: "+string(payload)+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	client := llm.New(srv.URL+"/v1", "test")
	r := testRunner(t, client, autoConfirm{})
	r.Profile = config.Builtin()
	r.Out = io.Discard
	r.Err = io.Discard
	code, _, _, _, esc := r.runAnswer(context.Background(), client, client, sysctx.Snapshot{}, nil, nil,
		"how to use types", agent.AnswerPrompt, false,
		Request{Plain: true}, intent.Decision{Intent: intent.Answer, Effort: effort}, 0)
	if code != 0 || esc != "" {
		t.Fatalf("code %d esc %q", code, esc)
	}
	return maxTok
}

func TestCutNoticeAndContinue(t *testing.T) {
	defer ui.ForceTheme(ui.Palette{})()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var mu sync.Mutex
	var bodies []string
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "test", "max_model_len": 32768}},
			})
			return
		}
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		n++
		call := n
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		var payload []byte
		if call == 1 {
			payload, _ = json.Marshal(map[string]any{
				"choices": []map[string]any{{
					"delta":         map[string]any{"content": "Python types start here.\n"},
					"finish_reason": "length",
				}},
				"usage": map[string]int{"prompt_tokens": 11, "completion_tokens": 600},
			})
		} else {
			payload, _ = json.Marshal(map[string]any{
				"choices": []map[string]any{{
					"delta": map[string]any{"content": "More on types.\n"},
				}},
				"usage": map[string]int{"prompt_tokens": 40, "completion_tokens": 20},
			})
		}
		_, _ = io.WriteString(w, "data: "+string(payload)+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	client := llm.New(srv.URL+"/v1", "test")
	r := testRunner(t, client, autoConfirm{})
	r.Profile = config.Builtin()
	var out bytes.Buffer
	r.Out = &out
	r.Err = io.Discard
	cwd := t.TempDir()
	res := r.RunTurn(context.Background(), Request{
		Text: "how to use types", Intent: "answer", Effort: "low", Yes: true, Cwd: cwd, New: true,
	})
	if res.ExitCode != 0 {
		t.Fatalf("cut exit %d %s", res.ExitCode, out.String())
	}
	if !strings.Contains(out.String(), "· cut at 600 tokens; ask 'continue' for more") {
		t.Fatalf("notice %q", out.String())
	}
	if strings.Contains(out.String(), "cut off") {
		t.Fatalf("error line %q", out.String())
	}
	if !strings.Contains(out.String(), "Python types start here.") {
		t.Fatalf("partial %q", out.String())
	}
	if !r.answerCut {
		t.Fatal("answerCut not set")
	}
	out.Reset()
	res = r.RunTurn(context.Background(), Request{Text: "continue", Yes: true, Cwd: cwd})
	if res.ExitCode != 0 {
		t.Fatalf("continue exit %d %s", res.ExitCode, out.String())
	}
	if res.Intent != "answer" {
		t.Fatalf("intent %s", res.Intent)
	}
	if r.answerCut {
		t.Fatal("cut flag stuck after a finished continue")
	}
	if !strings.Contains(out.String(), "More on types.") {
		t.Fatalf("resumed %q", out.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) < 2 {
		t.Fatalf("bodies %d", len(bodies))
	}
	var first struct {
		MaxTokens int `json:"max_tokens"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.MaxTokens != 600 {
		t.Fatalf("low cap %d", first.MaxTokens)
	}
	if !strings.Contains(bodies[1], "Python types start here.") || !strings.Contains(bodies[1], "continue from where it stopped") {
		t.Fatalf("continue did not resume the answer: %s", bodies[1])
	}
}
