package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/rag"
	"github.com/SniksaX/Konsolai/internal/safety"
)

func TestRecallReadOnlyAndFormat(t *testing.T) {
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hits := []rag.Hit{{
		Source:  "code",
		Ref:     json.RawMessage(`{"path":"internal/redact/redact.go","start_line":1,"end_line":10,"symbol":"Redact"}`),
		Content: "func Redact() {}",
	}}
	reg := NewRegistry(g, nil, nil, nil, nil, Options{
		Recall: func(context.Context, string, string, int) ([]rag.Hit, error) {
			return hits, nil
		},
	})
	tool, ok := reg.Get("recall")
	if !ok {
		t.Fatal("missing recall")
	}
	act, err := tool.Prepare(`{"query":"how are secrets redacted","scope":"code","k":4}`)
	if err != nil {
		t.Fatal(err)
	}
	if act.Kind() != ReadOnly {
		t.Fatal(act.Kind())
	}
	res, err := act.Run(context.Background())
	if err != nil || res.IsError {
		t.Fatal(res, err)
	}
	if res.Meta != "1 results" {
		t.Fatalf("meta %q", res.Meta)
	}
	if !strings.Contains(res.Output, "1. internal/redact/redact.go:1-10 Redact") {
		t.Fatalf("out %q", res.Output)
	}
}

func TestRecallRequiresQuery(t *testing.T) {
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool, _ := NewRegistry(g, nil, nil, nil, nil).Get("recall")
	if _, err := tool.Prepare(`{"scope":"all"}`); err == nil {
		t.Fatal("want query required")
	}
}
