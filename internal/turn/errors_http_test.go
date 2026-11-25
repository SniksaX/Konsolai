package turn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
)

func failingChatClient(t *testing.T, status int, body, key string) (*llm.Client, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := llm.New(srv.URL+"/v1", "m")
	c.PinContext(4096)
	c.SetAPIKey(func() (string, error) { return key, nil })
	return c, srv.URL + "/v1"
}

func TestRejectedRequestPrintsStatusAndProviderMessage(t *testing.T) {
	key := "sk" + "-abcdefghijklmnopqrstuvwx"
	body := `{"error":{"message":"bad key ` + key + ` (sent as Bearer ` + key + `)"}}`
	c, endpoint := failingChatClient(t, http.StatusUnauthorized, body, key)
	_, _, err := c.Complete(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil, llm.ChatOptions{})
	if err == nil {
		t.Fatal("want an error")
	}
	for _, trace := range []bool{false, true} {
		line := ModelErrorLine(endpoint, err, trace)
		if strings.Contains(line, key) {
			t.Fatalf("trace=%v leaked the key: %q", trace, line)
		}
		if !trace {
			if !strings.Contains(line, "HTTP 401") {
				t.Fatalf("status missing: %q", line)
			}
			if !strings.Contains(line, "bad key") {
				t.Fatalf("provider message missing: %q", line)
			}
			if strings.Contains(line, "{") {
				t.Fatalf("raw json printed: %q", line)
			}
		}
	}
}

func TestServerErrorPrintsStatusAndNestedMessage(t *testing.T) {
	c, endpoint := failingChatClient(t, http.StatusInternalServerError,
		`{"error":{"message":"upstream exploded","type":"server_error"}}`, "")
	_, _, err := c.Complete(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil, llm.ChatOptions{})
	if err == nil {
		t.Fatal("want an error")
	}
	line := ModelErrorLine(endpoint, err, false)
	if !strings.Contains(line, "HTTP 500") || !strings.Contains(line, "upstream exploded") {
		t.Fatalf("line %q", line)
	}
}

func TestRejectedRequestWithoutMessageStillNamesTheStatus(t *testing.T) {
	c, endpoint := failingChatClient(t, http.StatusBadRequest, `{}`, "")
	_, _, err := c.Complete(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil, llm.ChatOptions{})
	if err == nil {
		t.Fatal("want an error")
	}
	line := ModelErrorLine(endpoint, err, false)
	if !strings.Contains(line, "HTTP 400") || !strings.Contains(line, "no error message") {
		t.Fatalf("line %q", line)
	}
}

func TestRejectedRequestPrintsPlainTextBody(t *testing.T) {
	c, endpoint := failingChatClient(t, http.StatusTooManyRequests, "quota exceeded", "")
	_, _, err := c.Complete(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil, llm.ChatOptions{})
	if err == nil {
		t.Fatal("want an error")
	}
	line := ModelErrorLine(endpoint, err, false)
	if !strings.Contains(line, "HTTP 429") || !strings.Contains(line, "quota exceeded") {
		t.Fatalf("line %q", line)
	}
}
