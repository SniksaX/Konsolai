package commands

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/siraj/konsolai/internal/config"
	"github.com/siraj/konsolai/internal/embed"
	"github.com/siraj/konsolai/internal/memory"
	"github.com/siraj/konsolai/internal/store"
)

func TestParseRecallArgs(t *testing.T) {
	q, scope, ok := parseRecallArgs([]string{`where is the plan gate enforced`})
	if !ok || q != "where is the plan gate enforced" || scope != "all" {
		t.Fatalf("%q %q %v", q, scope, ok)
	}
	q, scope, ok = parseRecallArgs([]string{"how are secrets redacted", "--scope", "code"})
	if !ok || !strings.Contains(q, "secrets") || scope != "code" {
		t.Fatalf("%q %q %v", q, scope, ok)
	}
	if _, _, ok := parseRecallArgs(nil); ok {
		t.Fatal("empty")
	}
	if _, _, ok := parseRecallArgs([]string{"q", "--scope", "nope"}); ok {
		t.Fatal("bad scope")
	}
}

func TestRecallFormatting(t *testing.T) {
	u := os.Getenv("KONSOLAI_TEST_DATABASE_URL")
	if u == "" {
		t.Skip("KONSOLAI_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if !st.RAGAvailable(ctx) {
		t.Fatal("rag")
	}
	pid, err := st.UpsertProject(ctx, "/tmp/recall-fmt", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	vec := make([]float32, store.EmbeddingDim)
	vec[0] = 1
	content := "plan gate is enforced in internal/agent before mutating tools run " + time.Now().Format("20060102150405.000000000")
	ok, err := st.InsertChunk(ctx, store.ChunkIn{
		Profile:   "recalltest",
		ProjectID: &pid,
		Source:    "code",
		Ref:       json.RawMessage(`{"path":"internal/agent/agent.go","start_line":40,"end_line":80,"symbol":"Run"}`),
		Content:   content,
		Embedding: vec,
	})
	if err != nil || !ok {
		t.Fatalf("insert %v %v", ok, err)
	}
	embSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/embed") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([][]float32{vec})
	}))
	t.Cleanup(embSrv.Close)
	rankSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"index": 0, "score": 0.9}})
	}))
	t.Cleanup(rankSrv.Close)
	env, out, errb := testEnv(t)
	env.Profile = config.Profile{Name: "recalltest"}
	env.Mem = memory.NewGuard(st, nil)
	env.Embed = embed.New(embSrv.URL, rankSrv.URL)
	env.ProjectID = &pid
	if code := runRecall(ctx, env, []string{"where is the plan gate enforced", "--scope", "code"}); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "1. internal/agent/agent.go:40-80 Run") {
		t.Fatalf("format %q", got)
	}
	if !strings.Contains(got, "plan gate is enforced") {
		t.Fatalf("content %q", got)
	}
	_ = io.Discard
}
