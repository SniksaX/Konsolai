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
				"role": "assistant", "content": `{"intent":"act","language":"en","reason":"do it","reasoning":true}`,
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
	if d.Intent != Act || !d.Reasoning {
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
	if !strings.Contains(joined, "reasoning=true when the request needs multi-step analysis") {
		t.Fatal("router prompt missing reasoning rule")
	}
	if !strings.Contains(joined, "search online") || !strings.Contains(joined, "lookup") {
		t.Fatal("router prompt missing web-search rule")
	}
	for _, phrase := range []string{"this one", "this folder", "the code here", "ce dossier", "ici", "ce projet", "Never answer these without looking"} {
		if !strings.Contains(joined, phrase) {
			t.Fatalf("router prompt missing deictic %q", phrase)
		}
	}
	var wrap struct {
		JSONSchema struct {
			Schema struct {
				Required   []string                   `json:"required"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schema"`
		} `json:"json_schema"`
	}
	if err := json.Unmarshal(raw["response_format"], &wrap); err != nil {
		t.Fatal(err)
	}
	if _, ok := wrap.JSONSchema.Schema.Properties["reasoning"]; !ok {
		t.Fatal("schema missing reasoning")
	}
	if _, ok := wrap.JSONSchema.Schema.Properties["task"]; !ok {
		t.Fatal("schema missing task")
	}
	if _, ok := wrap.JSONSchema.Schema.Properties["continues_previous"]; !ok {
		t.Fatal("schema missing continues_previous")
	}
	found := false
	taskReq := false
	contReq := false
	for _, r := range wrap.JSONSchema.Schema.Required {
		if r == "reasoning" {
			found = true
		}
		if r == "task" {
			taskReq = true
		}
		if r == "continues_previous" {
			contReq = true
		}
	}
	if !found || !taskReq || !contReq {
		t.Fatalf("required %v", wrap.JSONSchema.Schema.Required)
	}
	if !strings.Contains(joined, `task is "analysis"`) {
		t.Fatal("router prompt missing task rule")
	}
	if !strings.Contains(joined, "continues_previous=true only if") {
		t.Fatal("router prompt missing continues_previous rule")
	}
}

func TestParseDecision(t *testing.T) {
	d, ok := parseDecision(`{"intent":"build","language":"fr","reason":"code","reasoning":false}`)
	if !ok || d.Intent != Build || d.Language != "fr" || d.Reasoning || d.Task != TaskOther {
		t.Fatalf("%+v %v", d, ok)
	}
	d, ok = parseDecision(`{"intent":"lookup","language":"en","reason":"review","reasoning":true,"task":"analysis","continues_previous":true}`)
	if !ok || d.Task != TaskAnalysis || !d.ContinuesPrevious {
		t.Fatalf("task %+v %v", d, ok)
	}
	d, ok = parseDecision(`{"intent":"answer","language":"en","reason":"debug","reasoning":true}`)
	if !ok || !d.Reasoning {
		t.Fatalf("%+v %v", d, ok)
	}
	if _, ok := parseDecision(`{"intent":"nope","language":"en","reason":"x"}`); ok {
		t.Fatal("invalid intent")
	}
}

func TestDeicticRoutingPhrases(t *testing.T) {
	for _, phrase := range []string{"this", "this one", "here", "this folder", "the code here", "ce dossier", "ici", "ce projet"} {
		if !strings.Contains(routerPrompt, phrase) {
			t.Fatalf("router missing %q", phrase)
		}
	}
	if !strings.Contains(routerPrompt, "Never answer these without looking") {
		t.Fatal("missing never-answer rule")
	}
}

func TestFallback(t *testing.T) {
	if g := fallback("what is go?", true, false); g.Intent != Lookup || g.ContinuesPrevious {
		t.Fatalf("%+v", g)
	}
	if g := fallback("what is go?", false, true); g.Intent != Answer || !g.ContinuesPrevious {
		t.Fatalf("%+v", g)
	}
	if g := fallback("create a folder", false, false); g.Intent != Act {
		t.Fatalf("%+v", g)
	}
}

func TestClassifyTellsNoPrevious(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{
				"role": "assistant", "content": `{"intent":"answer","language":"en","reason":"q","reasoning":false,"task":"other","continues_previous":false}`,
			}}},
		})
	}))
	t.Cleanup(srv.Close)
	c := llm.New(srv.URL+"/v1", "qwen")
	if _, err := Classify(context.Background(), c, "hi", nil, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gotBody), "There is no previous conversation.") {
		t.Fatalf("%s", gotBody)
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
