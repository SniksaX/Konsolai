package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// securityKey assembles a secret-shaped literal at runtime.
func securityKey() string { return "sk" + "-mcp-" + "0a1b2c3d" }

func captureOutput(t *testing.T, fn func()) string {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout, os.Stderr = pw, pw
	defer func() {
		os.Stdout, os.Stderr = oldOut, oldErr
	}()
	fn()
	pw.Close()
	b, _ := io.ReadAll(pr)
	pr.Close()
	return string(b)
}

func TestStdioChildEnvIsExact(t *testing.T) {
	t.Setenv("KONSOLAI_MCP_FROM_ENV", "resolved-value")
	t.Setenv("KONSOLAI_MCP_AMBIENT_SECRET", "ambient-"+securityKey())
	projectDir := t.TempDir()
	spec := stdioSpec(t, "basic", map[string]string{
		"KONSOLAI_MCP_DECLARED": "declared-${KONSOLAI_MCP_FROM_ENV}",
	})
	m, err := NewManager([]Spec{spec}, Options{ProjectDir: projectDir})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = m.CloseAll() })
	res, err := m.Call(context.Background(), "fake", "envdump", nil)
	if err != nil {
		t.Fatalf("envdump: %v", err)
	}
	var report struct {
		Env []string
		Cwd string
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &report); err != nil {
		t.Fatalf("decode envdump: %v", err)
	}
	env := map[string]string{}
	for _, entry := range report.Env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("bad env entry %q", entry)
		}
		env[key] = value
	}
	want := []string{"HOME", "KONSOLAI_MCP_DECLARED", "KONSOLAI_MCP_FAKE", "PATH"}
	got := make([]string, 0, len(env))
	for key := range env {
		got = append(got, key)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("child env keys %v, want exactly %v", got, want)
	}
	if env["KONSOLAI_MCP_DECLARED"] != "declared-resolved-value" {
		t.Fatalf("declared env %q", env["KONSOLAI_MCP_DECLARED"])
	}
	if _, leaked := env["KONSOLAI_MCP_AMBIENT_SECRET"]; leaked {
		t.Fatalf("the child inherited an ambient secret: %v", got)
	}
	if strings.Contains(strings.Join(report.Env, "\n"), securityKey()) {
		t.Fatal("the child saw an ambient secret value")
	}
	if resolved, _ := filepath.EvalSymlinks(projectDir); resolved != "" {
		gotCwd, _ := filepath.EvalSymlinks(report.Cwd)
		if gotCwd != resolved {
			t.Fatalf("child cwd %q, want %q", report.Cwd, projectDir)
		}
	}
}

func TestUnresolvedEnvVariableNamesTheVariable(t *testing.T) {
	t.Setenv("KONSOLAI_MCP_MISSING_ENV", "")
	os.Unsetenv("KONSOLAI_MCP_MISSING_ENV")
	spec := stdioSpec(t, "basic", map[string]string{
		"KONSOLAI_MCP_BROKEN": "${KONSOLAI_MCP_MISSING_ENV}",
	})
	m, err := NewManager([]Spec{spec}, Options{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = m.CloseAll() })
	_, err = m.Tools(context.Background(), "fake")
	if err == nil || !strings.Contains(err.Error(), "KONSOLAI_MCP_MISSING_ENV") {
		t.Fatalf("unresolved env error %v", err)
	}
	if strings.Contains(err.Error(), "KONSOLAI_MCP_BROKEN=") {
		t.Fatalf("the error includes a value: %v", err)
	}
}

func TestUnresolvedHeaderNamesTheVariable(t *testing.T) {
	t.Setenv("KONSOLAI_MCP_MISSING_HEADER", "")
	os.Unsetenv("KONSOLAI_MCP_MISSING_HEADER")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(ts.Close)
	spec := Spec{
		Name:      "fake",
		Transport: TransportHTTP,
		URL:       ts.URL,
		Headers:   map[string]string{"X-Token": "${KONSOLAI_MCP_MISSING_HEADER}"},
		Enabled:   true,
		Timeout:   2 * time.Second,
	}
	m, err := NewManager([]Spec{spec}, Options{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = m.CloseAll() })
	_, err = m.Tools(context.Background(), "fake")
	if err == nil || !strings.Contains(err.Error(), "KONSOLAI_MCP_MISSING_HEADER") {
		t.Fatalf("unresolved header error %v", err)
	}
}

func TestHTTPHeadersResolvedAtRequestTime(t *testing.T) {
	key := securityKey()
	t.Setenv("KONSOLAI_MCP_TOKEN", key)
	var mu sync.Mutex
	seen := map[string]string{}
	srv := newFakeServer("http")
	inner := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if value := r.Header.Get("X-Token"); value != "" {
			seen["X-Token"] = value
		}
		mu.Unlock()
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	spec := Spec{
		Name:      "fake",
		Transport: TransportHTTP,
		URL:       ts.URL,
		Headers:   map[string]string{"X-Token": "${KONSOLAI_MCP_TOKEN}"},
		Enabled:   true,
		Timeout:   10 * time.Second,
	}
	m, err := NewManager([]Spec{spec}, Options{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = m.CloseAll() })
	if _, err := m.Tools(context.Background(), "fake"); err != nil {
		t.Fatalf("tools: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if seen["X-Token"] != key {
		t.Fatalf("resolved header %q", seen["X-Token"])
	}
}

func TestPlainHTTPHeaderRefusal(t *testing.T) {
	secret := securityKey()
	spec := Spec{
		Name:      "remote",
		Transport: TransportHTTP,
		URL:       "http://203.0.113.9:81/mcp",
		Headers:   map[string]string{"Authorization": "Bearer " + secret},
		Enabled:   true,
		Timeout:   2 * time.Second,
	}
	m, err := NewManager([]Spec{spec}, Options{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = m.CloseAll() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, err = m.Tools(ctx, "remote")
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "plain HTTP") {
		t.Fatalf("plain HTTP header error %v", err)
	}
	if !strings.Contains(err.Error(), "Authorization") {
		t.Fatalf("the error does not name the header: %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("the error leaks the header value: %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("the refusal dialed the host first: %s", elapsed)
	}
}

func TestCrossHostRedirectRefusedAndAuthNotForwarded(t *testing.T) {
	secret := securityKey()
	var targetHits atomic.Int32
	var targetAuth atomic.Value
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		targetAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)

	spec := Spec{
		Name:      "redirect",
		Transport: TransportHTTP,
		URL:       source.URL,
		Headers:   map[string]string{"Authorization": "Bearer " + secret},
		Enabled:   true,
		Timeout:   5 * time.Second,
	}
	m, err := NewManager([]Spec{spec}, Options{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = m.CloseAll() })
	_, err = m.Tools(context.Background(), "redirect")
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("cross-host redirect error %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("the redirect error leaks the key: %v", err)
	}
	if targetHits.Load() != 0 {
		t.Fatalf("the redirect reached the other host %d times", targetHits.Load())
	}
	if auth, _ := targetAuth.Load().(string); auth != "" {
		t.Fatalf("the other host saw %q", auth)
	}
}

func TestSecretEchoIsReturnedRawAndNeverPrinted(t *testing.T) {
	secret := securityKey()
	spec := stdioSpec(t, "basic", map[string]string{"KONSOLAI_MCP_FAKE_SECRET": secret})
	m, err := NewManager([]Spec{spec}, Options{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = m.CloseAll() })
	var result Result
	var callErr error
	printed := captureOutput(t, func() {
		if _, err := m.Tools(context.Background(), "fake"); err != nil {
			callErr = err
			return
		}
		result, callErr = m.Call(context.Background(), "fake", "secret", nil)
		if callErr == nil {
			// An unknown tool is an error path; it must not print the value either.
			_, callErr = m.Call(context.Background(), "fake", "no-such-tool", nil)
		}
	})
	if callErr == nil {
		t.Fatalf("the unknown tool call succeeded")
	}
	if len(result.Content) != 1 || result.Content[0].Text != secret {
		t.Fatalf("the raw result was not passed through: %+v", result)
	}
	if printed != "" {
		t.Fatalf("the manager wrote to stdout/stderr: %q", printed)
	}
	if strings.Contains(callErr.Error(), secret) {
		t.Fatalf("the error leaks the secret: %v", callErr)
	}
}

func TestManagerNameAndShutdownDoNotTouchRealConfig(t *testing.T) {
	// The package must stay usable without the config package: construct a
	// manager from bare specs and close it cleanly.
	spec := Spec{Name: "solo", Transport: TransportHTTP, URL: "http://127.0.0.1:1", Enabled: true, Timeout: time.Second}
	m, err := NewManager([]Spec{spec}, Options{})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if names := m.Names(); len(names) != 1 || names[0] != "solo" {
		t.Fatalf("names %v", names)
	}
	if err := m.CloseAll(); err != nil {
		t.Fatalf("close all: %v", err)
	}
}
