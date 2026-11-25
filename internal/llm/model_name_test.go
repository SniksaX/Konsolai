package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func servedListServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"id": "deepseek-flash", "max_model_len": 4096},
			{"id": "deepseek-reasoner", "max_model_len": 4096},
		}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAmbiguousIDNamesTheSetCommand(t *testing.T) {
	srv := servedListServer(t)
	c := New(srv.URL+"/v1", "")
	_, err := c.ResolveModel(context.Background())
	if err == nil {
		t.Fatal("want an ambiguity error")
	}
	if !strings.Contains(err.Error(), "run: /set model id <name> deepseek-flash") {
		t.Fatalf("no placeholder command: %v", err)
	}

	c = New(srv.URL+"/v1", "")
	c.SetModelName("deepseek")
	_, err = c.ResolveModel(context.Background())
	if err == nil {
		t.Fatal("want an ambiguity error")
	}
	if !strings.Contains(err.Error(), "run: /set model id deepseek deepseek-flash") {
		t.Fatalf("no configured-name command: %v", err)
	}
	if !strings.Contains(err.Error(), "deepseek-reasoner") {
		t.Fatalf("served ids missing: %v", err)
	}
}
