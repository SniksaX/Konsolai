package intent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/siraj/konsolai/internal/llm"
)

func TestClassifySchemaAndRecent(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{
				"role": "assistant", "content": `{"intent":"act","language":"en","reason":"do it"}`,
			}}},
		})
	}))
	t.Cleanup(srv.Close)
	c := llm.New(srv.URL+"/v1", "qwen")
	recent := []llm.Message{
		{Role: llm.RoleUser, Content: "add a Subtract function"},
		{Role: llm.RoleAssistant, Content: "I can do that"},
		{Role: llm.RoleTool, Content: "ignored"},
		{Role: llm.RoleUser, Content: "do it"},
	}
	d, err := Classify(context.Background(), c, "do it", recent, true)
	if err != nil {
		t.Fatal(err)
	}
	if d.Intent != Act {
		t.Fatalf("%+v", d)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(gotBody, &raw); err != nil {
		t.Fatal(err)
	}
	var rf map[string]json.RawMessage
	if err := json.Unmarshal(raw["response_format"], &rf); err != nil {
		t.Fatal(err)
	}
	if string(rf["type"]) != `"json_schema"` {
		t.Fatalf("type %s", rf["type"])
	}
	var msgs []llm.Message
	if err := json.Unmarshal(raw["messages"], &msgs); err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, m := range msgs {
		joined += m.Content + "\n"
	}
	if !strings.Contains(joined, "add a Subtract function") || !strings.Contains(joined, "do it") {
		t.Fatalf("recent not included: %q", joined)
	}
	if strings.Contains(joined, "ignored") {
		t.Fatal("tool message included")
	}
}

func TestParseDecision(t *testing.T) {
	d, ok := parseDecision(`{"intent":"build","language":"fr","reason":"code"}`)
	if !ok || d.Intent != Build || d.Language != "fr" {
		t.Fatalf("%+v %v", d, ok)
	}
	if _, ok := parseDecision(`{"intent":"nope","language":"en","reason":"x"}`); ok {
		t.Fatal("invalid intent")
	}
}

func TestFallback(t *testing.T) {
	if g := fallback("what is go?", true); g.Intent != Lookup {
		t.Fatalf("%+v", g)
	}
	if g := fallback("what is go?", false); g.Intent != Answer {
		t.Fatalf("%+v", g)
	}
	if g := fallback("create a folder", false); g.Intent != Act {
		t.Fatalf("%+v", g)
	}
}

func TestClassifyFallbackOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", 500)
	}))
	t.Cleanup(srv.Close)
	c := llm.New(srv.URL+"/v1", "qwen")
	d, err := Classify(context.Background(), c, "which go version?", nil, true)
	if err == nil {
		t.Fatal("want error")
	}
	if d.Intent != Lookup {
		t.Fatalf("fallback %+v", d)
	}
}
