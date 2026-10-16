package rag

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/siraj/konsolai/internal/embed"
	"github.com/siraj/konsolai/internal/store"
)

func TestMergeRRF(t *testing.T) {
	vec := []store.SearchHit{
		{ID: 1, Source: "turn", Content: "a"},
		{ID: 2, Source: "code", Content: "b"},
		{ID: 3, Source: "web", Content: "c"},
	}
	txt := []store.SearchHit{
		{ID: 2, Source: "code", Content: "b"},
		{ID: 4, Source: "turn", Content: "d"},
		{ID: 1, Source: "turn", Content: "a"},
	}
	got := mergeRRF(vec, txt, 60)
	if len(got) != 4 {
		t.Fatalf("len %d", len(got))
	}
	if got[0].ID != 2 {
		t.Fatalf("id 2 should lead RRF, got %+v", got)
	}
	if got[0].Score <= got[1].Score {
		t.Fatalf("not sorted %+v", got)
	}
}

func TestRerankThreshold(t *testing.T) {
	hits := []Hit{
		{ID: 1, Content: "keep", Score: 0.5},
		{ID: 2, Content: "drop", Score: 0.4},
		{ID: 3, Content: "mid", Score: 0.3},
	}
	r := fakeRerank{scores: []embed.Scored{
		{Index: 0, Score: 0.9},
		{Index: 2, Score: 0.2},
		{Index: 1, Score: 0.05},
	}}
	got := applyRerank(context.Background(), r, "q", hits, 6)
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
		t.Fatalf("%+v", got)
	}
	down := applyRerank(context.Background(), fakeRerank{err: context.Canceled}, "q", hits, 2)
	if len(down) != 2 || down[0].ID != 1 {
		t.Fatalf("unreachable should keep RRF %+v", down)
	}
}

func TestFormatHitsAndMemory(t *testing.T) {
	hits := []Hit{
		{Source: "code", Ref: json.RawMessage(`{"path":"internal/agent/agent.go","start_line":10,"end_line":20,"symbol":"Run"}`), Content: "func Run() {}"},
		{Source: "turn", Ref: json.RawMessage(`{"title":"plan","date":"2026-10-16"}`), Content: "we chose 0.1"},
		{Source: "web", Ref: json.RawMessage(`{"url":"https://ex","title":"Ex"}`), Content: "page"},
	}
	out := FormatHits(hits)
	if !strings.Contains(out, "1. internal/agent/agent.go:10-20 Run") {
		t.Fatalf("code line %q", out)
	}
	if !strings.Contains(out, "plan · 2026-10-16") || !strings.Contains(out, "Ex https://ex") {
		t.Fatalf("refs %q", out)
	}
	msg := PrependMemory("what last time?", hits[:1])
	if !strings.Contains(msg, "<memory>") || !strings.Contains(msg, memoryNote) || !strings.HasSuffix(msg, "what last time?") {
		t.Fatalf("%q", msg)
	}
}

type fakeRerank struct {
	scores []embed.Scored
	err    error
}

func (f fakeRerank) Rerank(context.Context, string, []string) ([]embed.Scored, error) {
	return f.scores, f.err
}

type scopeStore struct {
	avail bool
	hits  []store.SearchHit
}

func (s *scopeStore) RAGAvailable(context.Context) bool { return s.avail }
func (s *scopeStore) SearchVector(_ context.Context, _ string, _ *int64, scope string, _ []float32, _ int) ([]store.SearchHit, error) {
	return filterScope(s.hits, scope), nil
}
func (s *scopeStore) SearchText(_ context.Context, _ string, _ *int64, scope, _ string, _ int) ([]store.SearchHit, error) {
	return filterScope(s.hits, scope), nil
}

func filterScope(hits []store.SearchHit, scope string) []store.SearchHit {
	var out []store.SearchHit
	for _, h := range hits {
		switch scope {
		case "sessions":
			if h.Source == "turn" || h.Source == "summary" {
				out = append(out, h)
			}
		case "code":
			if h.Source == "code" {
				out = append(out, h)
			}
		case "web":
			if h.Source == "web" {
				out = append(out, h)
			}
		default:
			out = append(out, h)
		}
	}
	return out
}

func TestSearchScopeFilter(t *testing.T) {
	st := &scopeStore{avail: true, hits: []store.SearchHit{
		{ID: 1, Source: "turn", Content: "session note"},
		{ID: 2, Source: "code", Content: "func Gate()"},
		{ID: 3, Source: "web", Content: "docs"},
	}}
	emb := &fakeEmbed{}
	got, err := Search(context.Background(), st, emb, fakeRerank{err: context.Canceled}, "home", nil, "gate", "code", 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Source != "code" {
		t.Fatalf("%+v", got)
	}
	got, err = Search(context.Background(), st, emb, nil, "home", nil, "note", "sessions", 6)
	if err != nil || len(got) != 1 || got[0].Source != "turn" {
		t.Fatalf("sessions %+v %v", got, err)
	}
}
