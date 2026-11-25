package turn

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/agent"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
)

// costLLM streams an answer that reports the usage the cost line is priced
// from. A request with an explicit intent skips the router.
func costLLM(t *testing.T) *llm.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "test", "max_model_len": 8192}},
		})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		delta, _ := json.Marshal(map[string]any{"choices": []map[string]any{{
			"delta":         map[string]any{"content": "hello"},
			"finish_reason": "stop",
		}}})
		usage, _ := json.Marshal(map[string]any{
			"choices": []any{},
			"usage":   map[string]any{"prompt_tokens": 2000, "completion_tokens": 1000},
		})
		_, _ = io.WriteString(w, "data: "+string(delta)+"\n\n")
		_, _ = io.WriteString(w, "data: "+string(usage)+"\n\ndata: [DONE]\n\n")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return llm.New(srv.URL+"/v1", "test")
}

func TestRunTurnPrintsTheRemoteCost(t *testing.T) {
	config.ResetRemoteState()
	t.Cleanup(config.ResetRemoteState)
	cases := []struct {
		name    string
		model   config.Model
		want    string
		unwants string
	}{
		{"remote priced", config.Model{Endpoint: "http://203.0.113.9/v1", Model: "m", PriceIn: 2.5, PriceOut: 10}, "$0.015", ""},
		{"local priced", config.Model{Endpoint: "http://127.0.0.1:9/v1", PriceIn: 2.5, PriceOut: 10}, "", "$"},
		{"remote unpriced", config.Model{Endpoint: "http://203.0.113.9/v1", Model: "m"}, "", "$"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := testRunner(t, costLLM(t), autoConfirm{})
			var errB bytes.Buffer
			r.Err = &errB
			c := config.Builtin()
			c.Models["api"] = tc.model
			c.Roles.Main = "api"
			c.Privacy.AllowRemote = true
			r.Profile = c
			res := r.RunTurn(context.Background(), Request{Text: "hi", Intent: "answer", Yes: true, Cwd: t.TempDir(), New: true})
			if res.ExitCode != 0 {
				t.Fatalf("exit %d: %s", res.ExitCode, errB.String())
			}
			out := errB.String()
			if tc.want != "" && !strings.Contains(out, tc.want) {
				t.Fatalf("summary missing %q: %q", tc.want, out)
			}
			if tc.unwants != "" && strings.Contains(out, tc.unwants) {
				t.Fatalf("summary has %q: %q", tc.unwants, out)
			}
		})
	}
}

func TestTurnPricingPicksTheServingModel(t *testing.T) {
	r := &Runner{Profile: config.Builtin()}
	r.Profile.Models["api"] = config.Model{Endpoint: "http://203.0.113.9/v1", PriceIn: 1.5, PriceOut: 6}
	r.Profile.Models["mini"] = config.Model{Endpoint: "http://203.0.113.10/v1", PriceIn: 0.5, PriceOut: 2}
	r.Profile.Models["home"] = config.Model{Endpoint: "http://127.0.0.1:9/v1", PriceIn: 1, PriceOut: 1}
	r.Profile.Roles.Main = "api"
	r.Profile.Roles.Fast = "mini"

	if got := r.turnPricing(true); !got.Remote || got.PriceIn != 1.5 || got.PriceOut != 6 {
		t.Fatalf("main pricing %+v", got)
	}
	if got := r.turnPricing(false); !got.Remote || got.PriceIn != 0.5 || got.PriceOut != 2 {
		t.Fatalf("fast pricing %+v", got)
	}
	r.Profile.Roles.Main = "home"
	if got := r.turnPricing(true); got != (agent.Pricing{}) {
		t.Fatalf("local pricing %+v", got)
	}
}
