package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func authServer(t *testing.T, auth *string, body *map[string]any, hits *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if hits != nil {
			*hits++
		}
		if auth != nil {
			*auth = r.Header.Get("Authorization")
		}
		if body != nil {
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClientSendsBearerKey(t *testing.T) {
	var auth string
	srv := authServer(t, &auth, nil, nil)
	c := New(srv.URL+"/v1", "m")
	c.PinContext(4096)
	c.SetAPIKey(func() (string, error) { return "sk" + "-test-key", nil })
	if _, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sk"+"-test-key" {
		t.Fatalf("authorization %q", auth)
	}
}

func TestMissingKeyFailsBeforeRequest(t *testing.T) {
	hits := 0
	srv := authServer(t, nil, nil, &hits)
	c := New(srv.URL+"/v1", "m")
	c.PinContext(4096)
	c.SetAPIKey(func() (string, error) {
		return "", errors.New("config: model \"m\": KONSOLAI_TEST_KEY is not set and no key is stored")
	})
	_, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{})
	if err == nil || !strings.Contains(err.Error(), "KONSOLAI_TEST_KEY") {
		t.Fatalf("err %v", err)
	}
	if hits != 0 {
		t.Fatalf("request sent without a key: %d", hits)
	}
}

func TestProviderQuirksInBody(t *testing.T) {
	var body map[string]any
	srv := authServer(t, nil, &body, nil)
	c := New(srv.URL+"/v1", "m")
	c.PinContext(4096)
	c.SetMaxTokensField("max_completion_tokens")
	c.SetSendTemperature(false)
	if _, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{
		MaxTokens: 7, Temperature: 0.5,
	}); err != nil {
		t.Fatal(err)
	}
	if body["max_completion_tokens"] != float64(7) {
		t.Fatalf("max_completion_tokens %v", body["max_completion_tokens"])
	}
	if _, ok := body["max_tokens"]; ok {
		t.Fatal("max_tokens must be omitted")
	}
	if _, ok := body["temperature"]; ok {
		t.Fatal("temperature must be omitted")
	}

	body = nil
	c2 := New(srv.URL+"/v1", "m")
	c2.PinContext(4096)
	if _, _, err := c2.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{
		MaxTokens: 7, Temperature: 0.5,
	}); err != nil {
		t.Fatal(err)
	}
	if body["max_tokens"] != float64(7) || body["temperature"] != 0.5 {
		t.Fatalf("default body %v", body)
	}
}
