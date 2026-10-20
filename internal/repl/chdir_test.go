package repl

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/memory"
	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/shellmode"
	"github.com/SniksaX/Konsolai/internal/store"
	"github.com/SniksaX/Konsolai/internal/turn"
)

func TestSwitchDirMovesTheProject(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	writeRepo(t, a, "alpha", "OLD_MARKER")
	writeRepo(t, b, "beta", "NEW_MARKER")
	sub := filepath.Join(a, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	start, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(start) })
	if err := os.Chdir(a); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	st := &session{cwd: a, id: "stay", mode: shellmode.New(a)}
	g, err := safety.NewAt(a, a)
	if err != nil {
		t.Fatal(err)
	}
	st.mode.Guard = g
	env := &commands.Env{Cwd: a, Out: &out, Profile: config.Profile{Name: "chdir"}}
	if err := st.switchDir(env, sub); err != nil {
		t.Fatal(err)
	}
	if st.id != "stay" || st.resumeIdle {
		t.Fatalf("subdir reset the session id=%q idle=%v", st.id, st.resumeIdle)
	}
	if strings.Contains(out.String(), "now in") {
		t.Fatalf("subdir announced a project change %q", out.String())
	}
	if err := st.switchDir(env, b); err != nil {
		t.Fatal(err)
	}
	if st.id != "" || !st.resumeIdle {
		t.Fatalf("project change id=%q idle=%v", st.id, st.resumeIdle)
	}
	if st.mode.Guard.Root() != evalDir(t, b) || st.mode.Guard.Base() != evalDir(t, b) {
		t.Fatalf("guard root %q base %q", st.mode.Guard.Root(), st.mode.Guard.Base())
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if wd != evalDir(t, b) {
		t.Fatalf("getcwd %q", wd)
	}
	got, err := st.mode.Guard.ResolveRead("MARKER")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "NEW_MARKER") {
		t.Fatalf("resolved %q", body)
	}
	if !strings.Contains(out.String(), "now in "+filepath.Base(b)+" (beta)") {
		t.Fatalf("line %q", out.String())
	}
	foot := formatFooter(footerInfo{Cwd: st.cwd, Model: "m", Routing: "main"})
	if !strings.Contains(foot, "beta") {
		t.Fatalf("footer %q", foot)
	}
	out.Reset()
	if err := st.switchDir(env, a); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "now in "+filepath.Base(a)+" (alpha)") {
		t.Fatalf("back %q", out.String())
	}
	foot = formatFooter(footerInfo{Cwd: st.cwd, Model: "m", Routing: "main"})
	if !strings.Contains(foot, "alpha") || strings.Contains(foot, "beta") {
		t.Fatalf("footer back %q", foot)
	}
	wd, err = os.Getwd()
	if err != nil || wd != evalDir(t, a) {
		t.Fatalf("back getcwd %q err=%v", wd, err)
	}
}

func TestShellCdThenTurnUsesTheNewProject(t *testing.T) {
	raw := os.Getenv("KONSOLAI_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("KONSOLAI_TEST_DATABASE_URL not set")
	}
	a := t.TempDir()
	b := t.TempDir()
	writeRepo(t, a, "alpha", "OLD_MARKER")
	writeRepo(t, b, "beta", "NEW_MARKER")
	start, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(start) })
	if err := os.Chdir(a); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	st := openChdirStore(t, raw)
	ctx := context.Background()
	pidB, err := st.UpsertProject(ctx, b, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := st.CreateSession(ctx, store.NewSession{
		Profile: "chdir", ProjectID: &pidB, CWD: b, Mode: "lookup", Model: "test", Title: "beta-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendMessage(ctx, seeded, 1, store.Message{Role: "user", Content: "earlier"}); err != nil {
		t.Fatal(err)
	}

	var seen chatCapture
	srv := chdirModel(t, &seen)
	prof := config.Builtin()
	prof.Name = "chdir"
	prof.Roles.Fast = ""
	prof.Roles.Embed = ""
	prof.Roles.Rerank = ""
	prof.RAG.Enabled = false
	prof.Database.URL = ""
	prof.UI.Banner = false
	prof.UI.ClearOnStart = false
	prof.Memory.SessionIdleMinutes = 30
	prof.SetMain(srv.URL+"/v1", "test", false)
	guard := memory.NewGuard(st, func(err error) { t.Errorf("memory: %v", err) })
	var out, errb bytes.Buffer
	env := &commands.Env{
		Profile: prof, Out: &out, Err: &errb, Cwd: a, Yes: true, Mem: guard, Version: "test",
	}
	runner := &turn.Runner{
		Profile: prof, Client: llm.New(srv.URL+"/v1", "test"), Mem: guard, Out: &out, Err: &errb,
	}
	code := Run(context.Background(), Config{
		Runner: runner, Env: env, Reg: commands.Default(),
		Reader: &scriptReader{lines: []string{
			"/cmd",
			"cd " + b,
			"exit",
			"read the marker",
			"/cd " + a,
			"read the marker again",
			"/exit",
		}},
		Version: "test", Model: "test", Out: &out, Err: &errb,
	})
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out.String(), errb.String())
	}
	text := out.String()
	sent := seen.join()
	newAt := strings.Index(sent, "NEW_MARKER")
	oldAt := strings.Index(sent, "OLD_MARKER")
	if newAt < 0 || oldAt < 0 || newAt > oldAt {
		t.Fatalf("markers\n%s\nerr %s\nsent %s", text, errb.String(), sent)
	}
	if !strings.Contains(text, "now in "+filepath.Base(b)+" (beta)") || !strings.Contains(text, "now in "+filepath.Base(a)+" (alpha)") {
		t.Fatalf("project lines\n%s", text)
	}
	msgs, err := st.LoadMessages(ctx, seeded)
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, m := range msgs {
		joined += m.Content + "\n"
	}
	if !strings.Contains(joined, "read the marker") {
		t.Fatalf("session %s did not resume\n%s", seeded, joined)
	}
	pidA, err := st.UpsertProject(ctx, a, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	back, err := st.NumberedSessions(ctx, "chdir", &pidA, a, 5, true)
	if err != nil || len(back) == 0 || evalDir(t, back[0].CWD) != evalDir(t, a) {
		t.Fatalf("back %+v err=%v", back, err)
	}
	wd, err := os.Getwd()
	if err != nil || wd != evalDir(t, a) {
		t.Fatalf("left shell in %q err=%v", wd, err)
	}
}

func writeRepo(t *testing.T, dir, branch, marker string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/"+branch+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "MARKER"), []byte(marker+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func evalDir(t *testing.T, dir string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

type chatCapture struct {
	mu    sync.Mutex
	parts []string
}

func (c *chatCapture) add(s string) {
	c.mu.Lock()
	c.parts = append(c.parts, s)
	c.mu.Unlock()
}

func (c *chatCapture) join() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.parts, "\n")
}

func chdirModel(t *testing.T, seen *chatCapture) *httptest.Server {
	t.Helper()
	var classifies, tools atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "chat/completions") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "test", "max_model_len": 8192}},
			})
			return
		}
		body, _ := io.ReadAll(r.Body)
		seen.add(string(body))
		if strings.Contains(string(body), "Classify the user's request") {
			n := classifies.Add(1)
			cont := 0
			if n == 1 {
				cont = 1
			}
			writeChat(w, llm.Message{Role: llm.RoleAssistant, Content: decisionJSON(cont)})
			return
		}
		if tools.Add(1)%2 == 1 {
			writeChat(w, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
				ID: "1", Type: "function", Function: llm.FunctionCall{Name: "read_file", Arguments: `{"path":"MARKER"}`},
			}}})
			return
		}
		writeChat(w, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			ID: "2", Type: "function", Function: llm.FunctionCall{Name: "finish", Arguments: `{"summary":"read it"}`},
		}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func decisionJSON(cont int) string {
	return `{"intent":"lookup","language":"en","reason":"read","reasoning":false,"effort":"low","task":"other","continues_session":` + itoa(cont) + `,"needs_memory":false}`
}

func writeChat(w http.ResponseWriter, msg llm.Message) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []map[string]any{{"message": msg}},
		"usage":   map[string]int{"prompt_tokens": 12, "completion_tokens": 4},
	})
}

func openChdirStore(t *testing.T, raw string) *store.Store {
	t.Helper()
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
	schema := "ktcd_" + hex.EncodeToString(tok[:])
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
	return st
}
