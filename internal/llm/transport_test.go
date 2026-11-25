package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func completionHandler(usage bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`
		if usage {
			body = `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`
		}
		_, _ = io.WriteString(w, body)
	}
}

func TestPlainHTTPKeyRefusedBeforeRequest(t *testing.T) {
	c := New("http://203.0.113.9/v1", "m")
	c.PinContext(4096)
	c.SetAPIKey(func() (string, error) { return "sk" + "-plain-http-key", nil })
	_, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{})
	if err == nil || !strings.Contains(err.Error(), "plain HTTP") || !strings.Contains(err.Error(), "203.0.113.9") {
		t.Fatalf("err %v", err)
	}
}

func TestRedirectStripsAuthorization(t *testing.T) {
	var targetAuth string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetAuth = r.Header.Get("Authorization")
		completionHandler(false)(w, r)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer source.Close()

	c := New(source.URL+"/v1", "m")
	c.PinContext(4096)
	c.SetAPIKey(func() (string, error) { return "sk" + "-redirect-key", nil })
	if _, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if targetAuth != "" {
		t.Fatalf("redirect carried the key: %q", targetAuth)
	}
}

func TestModelProbeRedirectStripsAuthorization(t *testing.T) {
	var targetAuth string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "probe-model", "max_model_len": 4096}}})
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer source.Close()

	c := New(source.URL+"/v1", "")
	c.SetAPIKey(func() (string, error) { return "sk" + "-probe-key", nil })
	if _, err := c.ResolveModel(context.Background()); err != nil {
		t.Fatal(err)
	}
	if targetAuth != "" {
		t.Fatalf("probe redirect carried the key: %q", targetAuth)
	}
}

func TestServerEchoDoesNotLeakKey(t *testing.T) {
	key := "sk" + "-echoed-in-body"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"bad key Authorization: Bearer `+key+`"}}`)
	}))
	defer srv.Close()
	c := New(srv.URL+"/v1", "m")
	c.PinContext(4096)
	c.SetAPIKey(func() (string, error) { return key, nil })
	_, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{})
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("error leaks the key: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("error not masked: %v", err)
	}
}

func TestUsageHookFeedsRemoteCap(t *testing.T) {
	srv := httptest.NewServer(completionHandler(true))
	defer srv.Close()
	c := New(srv.URL+"/v1", "m")
	c.PinContext(4096)
	var got int
	c.SetUsageHook(func(u Usage) { got = u.PromptTokens + u.CompletionTokens })
	if _, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if got != 5 {
		t.Fatalf("usage hook %d", got)
	}
}
