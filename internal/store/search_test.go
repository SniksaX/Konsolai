package store

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSearchScopeFilters(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	pid, err := s.UpsertProject(ctx, "/tmp/search-scope", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	insert := func(src, content string, dim int) {
		v := unitVec(dim)
		ok, err := s.InsertChunk(ctx, ChunkIn{
			Profile: "home", ProjectID: &pid, Source: src,
			Ref: json.RawMessage(`{"path":"p.go","title":"t"}`), Content: content, Embedding: v,
		})
		if err != nil || !ok {
			t.Fatalf("%s %v %v", src, ok, err)
		}
	}
	insert("turn", "session decision about rrf k sixty", 0)
	insert("code", "func Gate() unique-code-token", 1)
	insert("web", "fetched page unique-web-token", 2)

	code, err := s.SearchVector(ctx, "home", &pid, "code", unitVec(1), 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 1 || code[0].Source != "code" {
		t.Fatalf("code %+v", code)
	}
	txt, err := s.SearchText(ctx, "home", &pid, "web", "unique-web-token", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(txt) != 1 || txt[0].Source != "web" {
		t.Fatalf("web text %+v", txt)
	}
	sess, err := s.SearchText(ctx, "home", &pid, "sessions", "session decision", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess) != 1 || sess[0].Source != "turn" {
		t.Fatalf("sessions %+v", sess)
	}
}
