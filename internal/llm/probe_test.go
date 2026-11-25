package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// probeKey assembles a secret-shaped literal at runtime.
func probeKey() string { return "sk" + "-probe-" + "0a1b2c3d" }

func modelsServer(t *testing.T, ids ...string) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		data := make([]map[string]string, 0, len(ids))
		for _, id := range ids {
			data = append(data, map[string]string{"id": id})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestProbeSendsKeyAndListsIDs(t *testing.T) {
	key := probeKey()
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "deepseek-flash"}, {"id": "deepseek-reasoner"}}})
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "")
	c.SetAPIKey(func() (string, error) { return key, nil })
	res := c.Probe(context.Background())
	if res.Kind != ProbeOK {
		t.Fatalf("kind %s err %v", res.Kind, res.Err)
	}
	if auth != "Bearer "+key {
		t.Fatalf("probe did not send the key: %q", auth)
	}
	if strings.Join(res.IDs, ",") != "deepseek-flash,deepseek-reasoner" {
		t.Fatalf("ids %v", res.IDs)
	}
	if res.Endpoint != srv.URL+"/v1" {
		t.Fatalf("endpoint %q", res.Endpoint)
	}
}

func TestProbeLocalModelWithoutKeySendsNoAuthorization(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"data":[{"id":"local-model"}]}`)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "local-model")
	c.SetAPIKey(func() (string, error) { return "", nil })
	if res := c.Probe(context.Background()); res.Kind != ProbeOK {
		t.Fatalf("kind %s err %v", res.Kind, res.Err)
	}
	if auth != "" {
		t.Fatalf("a local model without a key sent %q", auth)
	}
}

func TestProbeAuthFailureMasksKey(t *testing.T) {
	key := probeKey()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"bad key Authorization: Bearer `+key+`"}}`)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "")
	c.SetAPIKey(func() (string, error) { return key, nil })
	res := c.Probe(context.Background())
	if res.Kind != ProbeAuthFailed || res.Status != http.StatusUnauthorized {
		t.Fatalf("kind %s status %d", res.Kind, res.Status)
	}
	if res.Err == nil || strings.Contains(res.Err.Error(), key) || !strings.Contains(res.Err.Error(), "[REDACTED]") {
		t.Fatalf("probe error leaked the key: %v", res.Err)
	}
	pe, ok := AsProbe(res.asError())
	if !ok || pe.Kind != ProbeAuthFailed || pe.Status != http.StatusUnauthorized {
		t.Fatalf("typed probe error %#v ok=%v", pe, ok)
	}
}

func TestProbeHTTPStatusIsNotUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "")
	res := c.Probe(context.Background())
	if res.Kind != ProbeHTTPError || res.Status != http.StatusBadGateway {
		t.Fatalf("kind %s status %d", res.Kind, res.Status)
	}
}

func TestProbeTimeoutIsSlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, `{"data":[{"id":"m"}]}`)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "")
	c.SetProbeTimeout(20 * time.Millisecond)
	res := c.Probe(context.Background())
	if res.Kind != ProbeSlow {
		t.Fatalf("kind %s err %v", res.Kind, res.Err)
	}
}

func TestProbeUnreachableIsDialFailure(t *testing.T) {
	c := New("http://127.0.0.1:1/v1", "")
	res := c.Probe(context.Background())
	if res.Kind != ProbeUnreachable {
		t.Fatalf("kind %s err %v", res.Kind, res.Err)
	}
}

func TestProbeBudgetKeepsLocalAndRemoteTimeouts(t *testing.T) {
	local := New("http://127.0.0.1:8000/v1", "")
	if got := local.probeBudget(); got != reachTimeout {
		t.Fatalf("local default budget %s", got)
	}
	local.SetProbeTimeout(45 * time.Millisecond)
	local.SetRemoteProbeTimeout(1234 * time.Millisecond)
	if got := local.probeBudget(); got != 45*time.Millisecond {
		t.Fatalf("local budget %s", got)
	}

	remote := New("https://203.0.113.9/v1", "")
	if got := remote.probeBudget(); got != remoteReachTimeout {
		t.Fatalf("remote default budget %s", got)
	}
	remote.SetProbeTimeout(45 * time.Millisecond)
	remote.SetRemoteProbeTimeout(1234 * time.Millisecond)
	if got := remote.probeBudget(); got != 1234*time.Millisecond {
		t.Fatalf("remote budget %s", got)
	}
	remote.SetRemoteProbeTimeout(0)
	if got := remote.probeBudget(); got != remoteReachTimeout {
		t.Fatalf("remote default budget after reset %s", got)
	}
}

func TestResolveModelEmptyIDUsesSingleServedModel(t *testing.T) {
	srv, _ := modelsServer(t, "deepseek-flash")
	c := New(srv.URL+"/v1", "")
	id, err := c.ResolveModel(context.Background())
	if err != nil || id != "deepseek-flash" {
		t.Fatalf("id %q err %v", id, err)
	}
}

func TestResolveModelAmbiguousListFailsWithServedIDs(t *testing.T) {
	srv, _ := modelsServer(t, "deepseek-chat", "deepseek-reasoner")
	c := New(srv.URL+"/v1", "")
	id, err := c.ResolveModel(context.Background())
	if err == nil {
		t.Fatalf("an ambiguous list resolved to %q", id)
	}
	for _, want := range []string{"serves 2 models", "deepseek-chat", "deepseek-reasoner", "set the model id"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q is missing %q", err, want)
		}
	}
	if c.ModelID() != "" {
		t.Fatalf("made-up model id %q", c.ModelID())
	}
}

func TestResolveModelConfiguredIDNotServedFails(t *testing.T) {
	srv, _ := modelsServer(t, "deepseek-flash")
	var chats int
	c := New(srv.URL+"/v1", "deepseek")
	c.SetAPIKey(func() (string, error) { return probeKey(), nil })
	id, err := c.ResolveModel(context.Background())
	if err == nil {
		t.Fatalf("a made-up id resolved to %q", id)
	}
	for _, want := range []string{`"deepseek"`, "not served", "deepseek-flash"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q is missing %q", err, want)
		}
	}
	// The chat path must not send a request for an id the server does not serve.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			chats++
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "deepseek-flash"}}})
	}))
	t.Cleanup(srv2.Close)
	c2 := New(srv2.URL+"/v1", "deepseek")
	c2.SetAPIKey(func() (string, error) { return probeKey(), nil })
	if _, _, err := c2.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{}); err == nil {
		t.Fatal("want a resolution error before the chat call")
	}
	if chats != 0 {
		t.Fatalf("chat calls %d", chats)
	}
}

func TestResolveModelAuthFailureIsProbeError(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "")
	c.SetAPIKey(func() (string, error) { return probeKey(), nil })
	_, err := c.ResolveModel(context.Background())
	if err == nil {
		t.Fatal("want a probe error")
	}
	pe, ok := AsProbe(err)
	if !ok || pe.Kind != ProbeAuthFailed || pe.Status != http.StatusUnauthorized {
		t.Fatalf("probe error %#v ok=%v", pe, ok)
	}
	if hits.Load() != 1 {
		t.Fatalf("models calls %d", hits.Load())
	}
}

func TestResolveModelKeyErrorIsProbeError(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "")
	c.SetAPIKey(func() (string, error) { return "", errNoTestKey{} })
	_, err := c.ResolveModel(context.Background())
	pe, ok := AsProbe(err)
	if !ok || pe.Kind != ProbeAuthFailed {
		t.Fatalf("probe error %#v ok=%v", pe, ok)
	}
	if hits.Load() != 0 {
		t.Fatalf("a request was sent despite the key error: %d", hits.Load())
	}
}

type errNoTestKey struct{}

func (errNoTestKey) Error() string { return "config: KONSOLAI_TEST_KEY is not set" }

func TestChatAuthFailureIsNotProbeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"nope"}}`)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "m")
	c.PinContext(4096)
	c.SetAPIKey(func() (string, error) { return probeKey(), nil })
	_, _, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{})
	if err == nil {
		t.Fatal("want an error")
	}
	if _, ok := AsProbe(err); ok {
		t.Fatalf("a chat failure was typed as a probe: %v", err)
	}
}

func TestNonStreamedReasoningContentCountsThinkTokens(t *testing.T) {
	reason := strings.Repeat("think ", 20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "content": "the answer", "reasoning_content": reason},
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 30},
		})
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "m")
	var progress Progress
	msg, usage, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{
		OnProgress: func(p Progress) { progress = p },
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg.Content, "think ") {
		t.Fatalf("reasoning leaked into the answer: %q", msg.Content)
	}
	if msg.Content != "the answer" {
		t.Fatalf("answer %q", msg.Content)
	}
	want := int(float64(len(reason))*defaultTokensPerChar + 0.5)
	if usage.ReasoningTokens != want || want == 0 {
		t.Fatalf("think tokens %d want %d", usage.ReasoningTokens, want)
	}
	if progress.ReasoningTokens != want {
		t.Fatalf("progress %+v", progress)
	}
}

func TestNonStreamedDeepSeekUsageThinkTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "content": "ok", "reasoning_content": "step by step"},
			}},
			"usage": map[string]any{
				"prompt_tokens": 12, "completion_tokens": 42, "total_tokens": 54,
				"prompt_cache_hit_tokens":   5,
				"prompt_cache_miss_tokens":  7,
				"completion_tokens_details": map[string]any{"reasoning_tokens": 31},
			},
		})
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "deepseek-reasoner")
	_, usage, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if usage.ReasoningTokens != 31 || usage.CompletionTokens != 42 || usage.PromptTokens != 12 {
		t.Fatalf("usage %+v", usage)
	}
}

func TestStreamedDeepSeekUsageThinkTokens(t *testing.T) {
	reason := strings.Repeat("r", 40)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w,
			`{"choices":[{"delta":{"reasoning_content":"`+reason+`"}}]}`,
			`{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":50,"completion_tokens_details":{"reasoning_tokens":44}}}`,
		)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "deepseek-reasoner")
	msg, usage, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "done" {
		t.Fatalf("answer %q", msg.Content)
	}
	if usage.ReasoningTokens != 44 {
		t.Fatalf("think tokens %d", usage.ReasoningTokens)
	}
}

func TestTopLevelReasoningTokensFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w,
			`{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":9,"reasoning_tokens":7}}`,
		)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/v1", "m")
	_, usage, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if usage.ReasoningTokens != 7 {
		t.Fatalf("think tokens %d", usage.ReasoningTokens)
	}
}

func TestPropsProbeSendsKey(t *testing.T) {
	var propsAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			_, _ = io.WriteString(w, `{"data":[{"id":"local-model"}]}`)
		case strings.HasSuffix(r.URL.Path, "/props"):
			propsAuth = r.Header.Get("Authorization")
			_, _ = io.WriteString(w, `{"default_generation_settings":{"n_ctx":4096}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	key := probeKey()
	c := New(srv.URL+"/v1", "")
	c.SetAPIKey(func() (string, error) { return key, nil })
	id, err := c.ResolveModel(context.Background())
	if err != nil || id != "local-model" {
		t.Fatalf("id %q err %v", id, err)
	}
	if c.ContextWindow() != 4096 {
		t.Fatalf("window %d", c.ContextWindow())
	}
	if propsAuth != "Bearer "+key {
		t.Fatalf("the props probe did not send the key: %q", propsAuth)
	}
}
