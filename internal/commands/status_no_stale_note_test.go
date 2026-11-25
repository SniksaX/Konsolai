package commands

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/turn"
)

// TestStatusKeepsNoStaleProbeNote covers the two ways a stale /models probe
// used to contradict a resolved model: the probe saw an ambiguous list before
// the client resolved one id, and a pinned client already knows its model.
func TestStatusKeepsNoStaleProbeNote(t *testing.T) {
	t.Run("first ambiguous then resolved", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasSuffix(r.URL.Path, "/models") {
				http.NotFound(w, r)
				return
			}
			if calls.Add(1) == 1 {
				_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-chat"},{"id":"deepseek-reasoner"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-flash","max_model_len":8192}]}`))
		}))
		t.Cleanup(srv.Close)

		env, out, _ := testEnv(t)
		env.Profile = config.WithMain(srv.URL+"/v1", "")
		env.Runner = &turn.Runner{Client: llm.New(srv.URL+"/v1", "")}
		if code := runStatus(context.Background(), env, nil); code != 0 {
			t.Fatalf("exit %d", code)
		}
		line := statusLine(t, out.String(), "model ")
		if !strings.Contains(line, "deepseek-flash") {
			t.Fatalf("the resolved model is missing: %q", line)
		}
		if strings.Contains(line, "not served") || strings.Contains(line, "server serves:") {
			t.Fatalf("a stale probe note survived the resolution: %q", line)
		}
	})

	t.Run("pinned client already resolved", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasSuffix(r.URL.Path, "/models") {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-chat"},{"id":"deepseek-reasoner"}]}`))
		}))
		t.Cleanup(srv.Close)

		env, out, _ := testEnv(t)
		env.Profile = config.WithMain(srv.URL+"/v1", "")
		client := llm.New(srv.URL+"/v1", "deepseek-flash")
		client.PinContext(8192)
		env.Runner = &turn.Runner{Client: client}
		if code := runStatus(context.Background(), env, nil); code != 0 {
			t.Fatalf("exit %d", code)
		}
		line := statusLine(t, out.String(), "model ")
		if !strings.Contains(line, "deepseek-flash") {
			t.Fatalf("the pinned model is missing: %q", line)
		}
		if strings.Contains(line, "not served") || strings.Contains(line, "server serves:") {
			t.Fatalf("a pinned resolved client kept the probe note: %q", line)
		}
	})
}
