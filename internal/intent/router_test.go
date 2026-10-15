package intent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/siraj/konsolai/internal/llm"
)

func TestClassifySchemaAndRecent(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{
				"role": "assistant", "content": `{"intent":"act","language":"en","reason":"do it","reasoning":true,"task":"other","continues_session":1}`,
			}}},
		})
	}))
	t.Cleanup(srv.Close)
	c := llm.New(srv.URL+"/v1", "qwen")
	cands := []Candidate{
		{Title: "add a Subtract function", LastUser: "do it", Age: time.Minute},
	}
	d, err := Classify(context.Background(), c, "do it", cands, true)
	if err != nil {
		t.Fatal(err)
	}
	if d.Intent != Act || !d.Reasoning || d.ContinuesSession != 1 {
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
	if !strings.Contains(joined, "add a Subtract function") || !strings.Contains(joined, "1.") {
		t.Fatalf("candidates not included: %q", joined)
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
	if _, ok := wrap.JSONSchema.Schema.Properties["continues_session"]; !ok {
		t.Fatal("schema missing continues_session")
	}
	if _, ok := wrap.JSONSchema.Schema.Properties["continues_previous"]; ok {
		t.Fatal("continues_previous should be gone")
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
		if r == "continues_session" {
			contReq = true
		}
	}
	if !found || !taskReq || !contReq {
		t.Fatalf("required %v", wrap.JSONSchema.Schema.Required)
	}
	if !strings.Contains(joined, `task is "analysis"`) || !strings.Contains(joined, `"docs"`) {
		t.Fatal("router prompt missing task rule")
	}
	if !strings.Contains(string(wrap.JSONSchema.Schema.Properties["task"]), `"docs"`) {
		t.Fatal("schema missing docs task")
	}
	if !strings.Contains(joined, "Pick the conversation this message follows up") {
		t.Fatal("router prompt missing continues_session rule")
	}
	if !strings.Contains(joined, "Prefer 0 when unsure") {
		t.Fatal("router prompt missing prefer-0 rule")
	}
}

func TestParseDecision(t *testing.T) {
	d, ok := parseDecision(`{"intent":"build","language":"fr","reason":"code","reasoning":false}`)
	if !ok || d.Intent != Build || d.Language != "fr" || d.Reasoning || d.Task != TaskOther || d.ContinuesSession != 0 {
		t.Fatalf("%+v %v", d, ok)
	}
	d, ok = parseDecision(`{"intent":"lookup","language":"en","reason":"review","reasoning":true,"task":"analysis","continues_session":2}`)
	if !ok || d.Task != TaskAnalysis || d.ContinuesSession != 2 {
		t.Fatalf("task %+v %v", d, ok)
	}
	d, ok = parseDecision(`{"intent":"build","language":"en","reason":"readme","reasoning":false,"task":"docs","continues_session":0}`)
	if !ok || d.Task != TaskDocs {
		t.Fatalf("docs %+v %v", d, ok)
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
	if g := fallback("what is go?", true); g.Intent != Lookup || g.ContinuesSession != 0 {
		t.Fatalf("%+v", g)
	}
	if g := fallback("what is go?", false); g.Intent != Answer || g.ContinuesSession != 0 {
		t.Fatalf("%+v", g)
	}
	if g := fallback("create a folder", false); g.Intent != Act {
		t.Fatalf("%+v", g)
	}
}

func TestClassifyTellsNoPrevious(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{
				"role": "assistant", "content": `{"intent":"answer","language":"en","reason":"q","reasoning":false,"task":"other","continues_session":0}`,
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
	if d.Intent != Lookup || d.ContinuesSession != 0 {
		t.Fatalf("fallback %+v", d)
	}
}

func TestFormatCandidates(t *testing.T) {
	got := FormatCandidates([]Candidate{
		{Title: "pdf build", LastUser: "create docs/OVERVIEW.pdf", Age: time.Minute},
		{Title: "how do I list git branches?", LastUser: "how do I list git branches?", Age: 5 * time.Minute},
	})
	if !strings.Contains(got, "1. [1m ago] pdf build") || !strings.Contains(got, "2. [5m ago] how do I list git branches?") {
		t.Fatalf("%q", got)
	}
}
