package turn

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/SniksaX/Konsolai/internal/agent"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/memory"
	"github.com/SniksaX/Konsolai/internal/store"
)

// The fake credential is assembled at runtime in each package's fixtures so a
// secret scanner never flags a literal, and it never matches a real key shape.
const leakKeyEnv = "KONSOLAI_LEAK_PROOF_KEY"

func leakKey() string {
	return "sk" + "-leak-" + "proof-" + "9f8e7d6c5b4a3210"
}

type leakCall struct {
	path    string
	headers http.Header
	body    string
}

type leakRecorder struct {
	mu    sync.Mutex
	calls []leakCall
}

func (r *leakRecorder) add(c leakCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
}

func (r *leakRecorder) snapshot() []leakCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]leakCall(nil), r.calls...)
}

// newLeakServer is an OpenAI-compatible fake. echoKey makes the chat endpoint
// echo the bearer key back in its 401 body, the way a misconfigured gateway can.
func newLeakServer(t *testing.T, key string, echoKey bool) (*httptest.Server, *leakRecorder) {
	t.Helper()
	rec := &leakRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.add(leakCall{path: r.URL.Path, headers: r.Header.Clone(), body: string(body)})
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"leak-model","max_model_len":8192}]}`)
			return
		}
		if echoKey {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, `{"error":{"message":"invalid key %s"}}`, key)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		content, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"delta": map[string]any{"content": "hello from the leak proof"}}},
		})
		usage, _ := json.Marshal(map[string]any{
			"choices": []any{},
			"usage":   map[string]int{"prompt_tokens": 11, "completion_tokens": 5},
		})
		fmt.Fprintf(w, "data: %s\n\n", content)
		fmt.Fprintf(w, "data: %s\n\n", usage)
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// leakProfile points the main role at endpoint and names the env var that holds
// the model's key. The second result is the model name used by the config and
// the secrets file.
func leakProfile(endpoint string) (config.Config, string) {
	c := config.WithMain(endpoint, "leak-model")
	name := c.Roles.Main
	m := c.Models[name]
	m.APIKeyEnv = leakKeyEnv
	c.Models[name] = m
	return c, name
}

// setLeakKey stores the key the way the surface under test reads it: the
// environment variable or the 0600 secrets file under a redirected HOME.
func setLeakKey(t *testing.T, mode, model, key string) {
	t.Helper()
	switch mode {
	case "env":
		t.Setenv(leakKeyEnv, key)
	case "secrets":
		t.Setenv("HOME", t.TempDir())
		if err := config.WriteSecret(model, key); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown key mode %q", mode)
	}
}

// assertAuthProvesKeyUsed proves the turn really carried the key: it was the
// only header that held it, every request authenticated with it, and no request
// body contained it.
func assertAuthProvesKeyUsed(t *testing.T, rec *leakRecorder, key string) {
	t.Helper()
	calls := rec.snapshot()
	if len(calls) == 0 {
		t.Fatal("the fake model saw no request")
	}
	chats := 0
	for _, c := range calls {
		if got := c.headers.Get("Authorization"); got != "Bearer "+key {
			t.Fatalf("%s: authorization %q", c.path, got)
		}
		for name, values := range c.headers {
			if name == "Authorization" {
				continue
			}
			for _, v := range values {
				if strings.Contains(v, key) {
					t.Fatalf("%s: header %s carries the key", c.path, name)
				}
			}
		}
		if strings.Contains(c.body, key) {
			t.Fatalf("%s: the request body carries the key", c.path)
		}
		if strings.HasSuffix(c.path, "/chat/completions") {
			chats++
		}
	}
	if chats == 0 {
		t.Fatal("the fake model saw no chat request")
	}
}

func assertNoLeak(t *testing.T, where, key, text string) {
	t.Helper()
	if strings.Contains(text, key) {
		t.Fatalf("%s leaked the model key", where)
	}
}

func leakTurnRunner(t *testing.T, prof config.Config, mem *memory.Guard, out, errw io.Writer) *Runner {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return &Runner{
		Profile: prof,
		Mem:     mem,
		Out:     out,
		Err:     errw,
		OpenConfirm: func(bool) (agent.Confirmer, func(), error) {
			return autoConfirm{}, func() {}, nil
		},
	}
}

// runLeakTurn runs one answer turn against the fake server in the given key
// mode. The caller decides whether the server answers or echoes the key back
// in a 401 body.
func runLeakTurn(t *testing.T, key, mode string, echo, trace bool, mem *memory.Guard, req Request) (Result, string, string, *leakRecorder) {
	t.Helper()
	srv, rec := newLeakServer(t, key, echo)
	prof, model := leakProfile(srv.URL + "/v1")
	setLeakKey(t, mode, model, key)
	resolved, err := prof.ModelAPIKey(model)
	if err != nil || resolved != key {
		t.Fatalf("the key did not resolve from mode %s: %v", mode, err)
	}
	var out, errb strings.Builder
	r := leakTurnRunner(t, prof, mem, &out, &errb)
	req.Trace = trace
	req.Intent = "answer"
	req.Yes = true
	req.New = true
	if req.Cwd == "" {
		req.Cwd = t.TempDir()
	}
	res := r.RunTurn(context.Background(), req)
	return res, out.String(), errb.String(), rec
}

func TestTraceDoesNotLeakModelKey(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, mode := range []string{"env", "secrets"} {
		t.Run(mode, func(t *testing.T) {
			key := leakKey()
			res, out, errb, rec := runLeakTurn(t, key, mode, false, true, memory.NewGuard(nil, nil), Request{Text: "say hello"})
			if res.ExitCode != 0 {
				t.Fatalf("exit %d: %s", res.ExitCode, errb)
			}
			assertAuthProvesKeyUsed(t, rec, key)
			if !strings.Contains(errb, "router ") || !strings.Contains(errb, "stats: steps=") {
				t.Fatalf("the turn-level trace is missing: %q", errb)
			}
			assertNoLeak(t, "trace stdout", key, out)
			assertNoLeak(t, "trace stderr", key, errb)
		})
	}
}

// TestTraceDoesNotLeakAnEchoedKey covers the raw-error branch of the trace:
// the model server echoes the bearer key in a 401 body, so the trace prints the
// error. The key must be masked, not printed verbatim.
func TestTraceDoesNotLeakAnEchoedKey(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, mode := range []string{"env", "secrets"} {
		t.Run(mode, func(t *testing.T) {
			key := leakKey()
			res, out, errb, rec := runLeakTurn(t, key, mode, true, true, memory.NewGuard(nil, nil), Request{Text: "say hello"})
			if res.ExitCode != 1 {
				t.Fatalf("exit %d: %s", res.ExitCode, errb)
			}
			assertAuthProvesKeyUsed(t, rec, key)
			if !strings.Contains(errb, "konsolai:") {
				t.Fatalf("the model error did not reach the trace: %q", errb)
			}
			assertNoLeak(t, "trace stdout", key, out)
			assertNoLeak(t, "trace stderr", key, errb)
		})
	}
}

func TestModelRequestsKeepTheKeyInTheHeaderOnly(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, mode := range []string{"env", "secrets"} {
		t.Run(mode, func(t *testing.T) {
			key := leakKey()
			res, out, errb, rec := runLeakTurn(t, key, mode, false, false, memory.NewGuard(nil, nil), Request{Text: "say hello"})
			if res.ExitCode != 0 {
				t.Fatalf("exit %d: %s", res.ExitCode, errb)
			}
			assertAuthProvesKeyUsed(t, rec, key)
			assertNoLeak(t, "turn output", key, out)
			assertNoLeak(t, "turn diagnostics", key, errb)
		})
	}
}

// TestNoLogLineCarriesTheModelKey documents the log surface. Konsolai imports
// no logging package (a grep for the standard log package finds no non-test
// use), so the turn's only diagnostic channels are the Runner's Out and Err
// writers. This test runs a full turn with tracing on and a model error that
// echoes the key, then proves neither writer emits a line containing the key.
func TestNoLogLineCarriesTheModelKey(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, mode := range []string{"env", "secrets"} {
		t.Run(mode, func(t *testing.T) {
			key := leakKey()
			res, out, errb, rec := runLeakTurn(t, key, mode, true, true, memory.NewGuard(nil, nil), Request{Text: "say hello"})
			if res.ExitCode != 1 {
				t.Fatalf("exit %d: %s", res.ExitCode, errb)
			}
			assertAuthProvesKeyUsed(t, rec, key)
			if !strings.Contains(errb, "[REDACTED]") {
				t.Fatalf("the echoed key should be masked: %q", errb)
			}
			for _, line := range strings.Split(out+"\n"+errb, "\n") {
				if strings.Contains(line, key) {
					t.Fatalf("a log line carries the model key: %q", line)
				}
			}
		})
	}
}

func openLeakDB(t *testing.T) (*store.Store, *pgxpool.Pool) {
	t.Helper()
	raw := os.Getenv("KONSOLAI_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("KONSOLAI_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var tok [6]byte
	if _, err := rand.Read(tok[:]); err != nil {
		t.Fatal(err)
	}
	schema := "ktleak_" + hex.EncodeToString(tok[:])
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+ident+" CASCADE")
	})
	st, err := store.OpenWith(ctx, raw, store.OpenOptions{SearchPath: schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET search_path TO "+ident+", public")
		return err
	}
	q, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(q.Close)
	return st, q
}

// leakStoreColumns is every text column a turn can write.
var leakStoreColumns = [][2]string{
	{"messages", "content"},
	{"messages", "tool_calls"},
	{"sessions", "title"},
	{"sessions", "model"},
	{"turn_stats", "model"},
	{"turn_stats", "endpoint"},
	{"actions", "args"},
	{"actions", "output"},
	{"actions", "reject_reason"},
	{"actions", "meta"},
	{"file_changes", "diff"},
	{"file_changes", "undo_blocked"},
	{"user_facts", "fact"},
	{"project_facts", "fact"},
	{"web_cache", "title"},
	{"web_cache", "text"},
	{"memory_chunks", "content"},
	{"memory_chunks", "ref"},
}

func assertNoKeyInStoreRows(t *testing.T, q *pgxpool.Pool, key string) {
	t.Helper()
	ctx := context.Background()
	for _, col := range leakStoreColumns {
		var n int
		query := "SELECT count(*) FROM " + col[0] + " WHERE " + col[1] + "::text ILIKE '%' || $1 || '%'"
		if err := q.QueryRow(ctx, query, key).Scan(&n); err != nil {
			t.Fatalf("scan %s.%s: %v", col[0], col[1], err)
		}
		if n != 0 {
			t.Fatalf("%s.%s holds the model key (%d rows)", col[0], col[1], n)
		}
	}
}

func waitTurnStats(t *testing.T, q *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		var n int
		if err := q.QueryRow(ctx, "SELECT count(*) FROM turn_stats").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no turn_stats row was persisted")
}

// TestStoreTranscriptsDoNotLeakModelKey runs a persisted turn and checks the
// transcripts API and every text column a turn can write for the key.
func TestStoreTranscriptsDoNotLeakModelKey(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	st, q := openLeakDB(t)
	for _, mode := range []string{"env", "secrets"} {
		t.Run(mode, func(t *testing.T) {
			key := leakKey()
			guard := memory.NewGuard(st, func(error) {})
			res, out, errb, rec := runLeakTurn(t, key, mode, false, false, guard, Request{Text: "say hello"})
			if res.ExitCode != 0 {
				t.Fatalf("exit %d: %s", res.ExitCode, errb)
			}
			assertAuthProvesKeyUsed(t, rec, key)
			assertNoLeak(t, "turn output", key, out)
			assertNoLeak(t, "turn diagnostics", key, errb)
			if res.SessionID == "" {
				t.Fatal("the turn did not create a session")
			}
			msgs, err := st.LoadMessages(context.Background(), res.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			var b strings.Builder
			for _, m := range msgs {
				b.WriteString(m.Content)
				b.WriteString("\n")
				b.Write(m.ToolCalls)
				b.WriteString("\n")
			}
			if !strings.Contains(b.String(), "hello from the leak proof") {
				t.Fatalf("the assistant answer was not persisted: %q", b.String())
			}
			assertNoLeak(t, "the transcripts API", key, b.String())
			waitTurnStats(t, q)
			assertNoKeyInStoreRows(t, q, key)
		})
	}
}
