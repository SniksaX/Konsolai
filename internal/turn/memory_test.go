package turn

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/intent"
	"github.com/SniksaX/Konsolai/internal/rag"
)

func TestNeedsMemoryInjection(t *testing.T) {
	if !intent.LooksLikeMemory("what did we decide about the rerank threshold last time?") {
		t.Fatal("router cue")
	}
	hits := []rag.Hit{{
		Source:  "turn",
		Ref:     json.RawMessage(`{"title":"memory","date":"2026-10-16"}`),
		Content: "we chose a rerank threshold of 0.1",
	}}
	got, n := AttachMemories("what did we decide about the rerank threshold last time?", hits)
	if n != 1 {
		t.Fatalf("n %d", n)
	}
	if !strings.Contains(got, "<memory>") || !strings.Contains(got, "Retrieved from past sessions; may be outdated.") {
		t.Fatalf("%q", got)
	}
	if !strings.Contains(got, "0.1") || !strings.HasSuffix(got, "what did we decide about the rerank threshold last time?") {
		t.Fatalf("%q", got)
	}
}
