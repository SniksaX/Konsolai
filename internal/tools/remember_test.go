package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/safety"
)

type memFacts struct {
	fact, scope string
}

func (m *memFacts) Add(_ context.Context, fact, scope string) error {
	m.fact, m.scope = fact, scope
	return nil
}

func TestRememberPrepareAndRun(t *testing.T) {
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &memFacts{}
	reg := NewRegistry(g, nil, nil, nil, f)
	tool, ok := reg.Get("remember")
	if !ok {
		t.Fatal("missing remember")
	}
	act, err := tool.Prepare(`{"fact":"I use pnpm"}`)
	if err != nil {
		t.Fatal(err)
	}
	if act.Kind() != Mutating {
		t.Fatal("remember must confirm")
	}
	res, err := act.Run(context.Background())
	if err != nil || res.IsError || f.fact != "I use pnpm" || f.scope != "user" {
		t.Fatalf("res=%+v fact=%q scope=%q err=%v", res, f.fact, f.scope, err)
	}
	act, err = tool.Prepare(`{"fact":"module demo","scope":"project"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := act.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.scope != "project" {
		t.Fatalf("scope %q", f.scope)
	}
	if _, err := tool.Prepare(`{"fact":""}`); err == nil || !strings.Contains(err.Error(), "fact") {
		t.Fatalf("empty fact: %v", err)
	}
}
