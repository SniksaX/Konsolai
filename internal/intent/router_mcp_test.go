package intent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
)

// TestClassifyWithServersPrompt proves the router gets one safe line per MCP
// server and no tool schema text at all.
func TestClassifyWithServersPrompt(t *testing.T) {
	var system string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"m","max_model_len":8192}]}`))
			return
		}
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, m := range body.Messages {
			if m.Role == "system" {
				system = m.Content
			}
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"{\"intent\":\"answer\",\"language\":\"en\",\"reason\":\"t\",\"reasoning\":false,\"effort\":\"low\",\"task\":\"other\",\"continues_session\":0,\"needs_memory\":false}"}}]}`)
	}))
	defer srv.Close()
	client := llm.New(srv.URL+"/v1", "m")
	client.PinContext(8192)
	lines := []string{"github: http, trust ask, enabled", "fs: stdio, trust read_only, disabled, private"}
	if _, err := ClassifyWithServers(context.Background(), client, "do it", nil, false, lines); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"MCP servers configured", "- github: http, trust ask, enabled", "- fs: stdio, trust read_only, disabled, private"} {
		if !strings.Contains(system, want) {
			t.Fatalf("router prompt misses %q:\n%s", want, system)
		}
	}
	for _, forbidden := range []string{`"type":"object"`, `"inputSchema"`, `"parameters"`, "propose_plan", "tool schema of"} {
		if strings.Contains(system, forbidden) {
			t.Fatalf("router prompt contains schema text %q", forbidden)
		}
	}
}

func TestClassifyWithoutServersKeepsTheOldPrompt(t *testing.T) {
	var system string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"m","max_model_len":8192}]}`))
			return
		}
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, m := range body.Messages {
			if m.Role == "system" {
				system = m.Content
			}
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"{\"intent\":\"answer\",\"language\":\"en\",\"reason\":\"t\",\"reasoning\":false,\"effort\":\"low\",\"task\":\"other\",\"continues_session\":0,\"needs_memory\":false}"}}]}`)
	}))
	defer srv.Close()
	client := llm.New(srv.URL+"/v1", "m")
	client.PinContext(8192)
	if _, err := Classify(context.Background(), client, "do it", nil, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(system, "MCP servers configured") {
		t.Fatal("a profile without MCP servers must keep the old prompt")
	}
}
