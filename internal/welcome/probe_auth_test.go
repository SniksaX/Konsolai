package welcome

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// welcomeKey assembles a secret-shaped literal at runtime.
func welcomeKey() string { return "sk" + "-welcome-" + "0a1b2c3d" }

func TestGatherSendsKeyToTheModelProbe(t *testing.T) {
	key := welcomeKey()
	var auth string
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-flash","max_model_len":128000}]}`))
	}))
	t.Cleanup(srv.Close)

	data, err := Gather(context.Background(), Input{
		Endpoint: srv.URL + "/v1",
		Model:    "deepseek-flash",
		CWD:      t.TempDir(),
		APIKey:   func(role string) (string, error) { return key, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/models" {
		t.Fatalf("probe path %q", path)
	}
	if auth != "Bearer "+key {
		t.Fatalf("welcome probe did not send the key: %q", auth)
	}
	if data.Models[0].Status != StatusUp {
		t.Fatalf("main row %+v", data.Models[0])
	}
}

func TestGatherAuthFailureReadsAsAuthFailed(t *testing.T) {
	key := welcomeKey()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad Authorization: Bearer ` + key + `"}}`))
	}))
	t.Cleanup(srv.Close)

	data, err := Gather(context.Background(), Input{
		Endpoint: srv.URL + "/v1",
		Model:    "deepseek-flash",
		CWD:      t.TempDir(),
		APIKey:   func(role string) (string, error) { return key, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	main := data.Models[0]
	if main.Status != StatusAuth || main.Stat != "auth failed" {
		t.Fatalf("a 401 must read as auth failed: %+v", main)
	}
	if data.Up != 0 {
		t.Fatalf("an auth failure counted as up: %d", data.Up)
	}
	if main.Stat == "unreachable" {
		t.Fatal("a 401 was reported unreachable")
	}
}

func TestGatherKeyErrorReadsAsAuthFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	t.Cleanup(srv.Close)
	data, err := Gather(context.Background(), Input{
		Endpoint: srv.URL + "/v1",
		Model:    "m",
		CWD:      t.TempDir(),
		APIKey:   func(role string) (string, error) { return "", errNoWelcomeKey{} },
	})
	if err != nil {
		t.Fatal(err)
	}
	if data.Models[0].Status != StatusAuth || data.Models[0].Stat != "auth failed" {
		t.Fatalf("a missing key must read as auth failed: %+v", data.Models[0])
	}
}

type errNoWelcomeKey struct{}

func (errNoWelcomeKey) Error() string { return "config: DEEPSEEK_API_KEY is not set" }

func TestGatherLocalModelWithoutKeySendsNoAuthorization(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"local-model"}]}`))
	}))
	t.Cleanup(srv.Close)
	data, err := Gather(context.Background(), Input{
		Endpoint: srv.URL + "/v1",
		Model:    "local-model",
		CWD:      t.TempDir(),
		APIKey:   func(role string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		t.Fatalf("a local model without a key sent %q", auth)
	}
	if data.Models[0].Status != StatusUp {
		t.Fatalf("main row %+v", data.Models[0])
	}
}

func TestGatherProbeTimeoutReadsAsSlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	t.Cleanup(srv.Close)
	data, err := Gather(context.Background(), Input{
		Endpoint:     srv.URL + "/v1",
		Model:        "m",
		CWD:          t.TempDir(),
		ProbeTimeout: 20 * time.Millisecond,
		ProbeBudget:  150 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	main := data.Models[0]
	if main.Status != StatusSlow || main.Stat != "slow" {
		t.Fatalf("a probe timeout must read as slow: %+v", main)
	}
}
