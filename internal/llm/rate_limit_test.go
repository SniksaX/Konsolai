package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type rateServer struct {
	mu      sync.Mutex
	hits    int
	status  int
	retry   string
	success bool
}

func newRateServer(t *testing.T, status int, retry string) (*Client, *rateServer) {
	t.Helper()
	rs := &rateServer{status: status, retry: retry}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[{"id":"m","max_model_len":8192}]}`))
			return
		}
		rs.mu.Lock()
		rs.hits++
		status := rs.status
		rs.mu.Unlock()
		if status == http.StatusTooManyRequests && rs.hits == 1 {
			if rs.retry != "" {
				w.Header().Set("Retry-After", rs.retry)
			}
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"rate limited"}}`)
			return
		}
		if status == http.StatusTooManyRequests {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
			return
		}
		if status >= 400 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":{"message":"refused"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "m")
	c.PinContext(8192)
	c.SetRetry(0, 1)
	return c, rs
}

func TestShortRetryAfterWaitsOnce(t *testing.T) {
	c, rs := newRateServer(t, http.StatusTooManyRequests, "2")
	var waited time.Duration
	var notices []int
	c.SetRateLimitHandlers(func(d time.Duration) { waited = d }, func(n int) { notices = append(notices, n) })
	if _, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "go"}}, nil, ChatOptions{}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if rs.hits != 2 {
		t.Fatalf("hits %d", rs.hits)
	}
	if waited != 2*time.Second || len(notices) != 1 || notices[0] != 2 {
		t.Fatalf("waited %s notices %v", waited, notices)
	}
}

func TestLongRetryAfterStopsWithExplanation(t *testing.T) {
	c, rs := newRateServer(t, http.StatusTooManyRequests, "60")
	c.SetRateLimitHandlers(func(time.Duration) {}, nil)
	_, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "go"}}, nil, ChatOptions{})
	if err == nil || !strings.Contains(err.Error(), "60") || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("want a plain explanation, got %v", err)
	}
	if rs.hits != 1 {
		t.Fatalf("hits %d", rs.hits)
	}
}

func TestAuthIsNeverRetried(t *testing.T) {
	c, rs := newRateServer(t, http.StatusUnauthorized, "1")
	c.SetRateLimitHandlers(func(time.Duration) {}, nil)
	_, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "go"}}, nil, ChatOptions{})
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusUnauthorized {
		t.Fatalf("want the 401, got %v", err)
	}
	if rs.hits != 1 {
		t.Fatalf("401 was retried: %d", rs.hits)
	}
}
