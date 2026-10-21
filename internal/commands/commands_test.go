package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/memory"
	"github.com/SniksaX/Konsolai/internal/turn"
	"github.com/SniksaX/Konsolai/internal/ui"
)

func testEnv(t *testing.T) (*Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, errb bytes.Buffer
	var copied string
	env := &Env{
		Profile: config.WithMain("http://127.0.0.1:9/v1", "m"),
		Out:     &out,
		Err:     &errb,
		Cwd:     t.TempDir(),
		Copy: func(_ context.Context, text string) (string, error) {
			copied = text
			return "fake", nil
		},
		Version: "test",
	}
	t.Cleanup(func() { _ = copied })
	return env, &out, &errb
}

func samplePairs() []histItem {
	return []histItem{{
		ID:     "s1",
		Title:  "one-liner",
		Mode:   "answer",
		Status: "finished",
		CWD:    "/tmp/ko",
		Msgs: []llm.Message{
			{Role: llm.RoleUser, Content: "biggest files?"},
			{Role: llm.RoleAssistant, Content: "use du:\n```bash\ndu -ah | sort -nr | head -3\n```\n"},
			{Role: llm.RoleUser, Content: "and ls?"},
			{Role: llm.RoleAssistant, Content: "```sh\nls -la\n```\n```python\nprint(1)\n```\n"},
		},
		ShellCmds: []string{"echo from-action"},
		Changes:   []fileChange{{Path: "a.txt", Diff: "--- a\n+++ b\n+hi\n"}},
	}}
}

func TestCopyAnswerAllCodeCmd(t *testing.T) {
	env, out, errb := testEnv(t)
	var got string
	env.Copy = func(_ context.Context, text string) (string, error) {
		got = text
		return "fake", nil
	}
	env.LoadHistory = func(context.Context, int) []histItem { return samplePairs() }

	if code := runCopy(context.Background(), env, []string{"answer"}); code != 0 {
		t.Fatalf("answer %d %s", code, errb.String())
	}
	if got != "```sh\nls -la\n```\n```python\nprint(1)\n```\n" {
		t.Fatalf("last answer %q", got)
	}
	if !strings.Contains(out.String(), "copied answer") || !strings.Contains(out.String(), "via fake") {
		t.Fatalf("print %q", out.String())
	}

	out.Reset()
	if code := runCopy(context.Background(), env, []string{"answer", "2"}); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(got, "\n\n---\n\n") || !strings.Contains(got, "use du:") {
		t.Fatalf("two answers %q", got)
	}

	out.Reset()
	if code := runCopy(context.Background(), env, []string{"all", "2"}); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(got, "### Prompt") || !strings.Contains(got, "biggest files?") || !strings.Contains(got, "### Answer") {
		t.Fatalf("all %q", got)
	}

	out.Reset()
	if code := runCopy(context.Background(), env, []string{"code"}); code != 0 {
		t.Fatal(code)
	}
	if got != "ls -la\n\nprint(1)" {
		t.Fatalf("code all %q", got)
	}
	if code := runCopy(context.Background(), env, []string{"code", "1"}); code != 0 {
		t.Fatal(code)
	}
	if got != "print(1)" {
		t.Fatalf("code last %q", got)
	}

	if code := runCopy(context.Background(), env, []string{"cmd"}); code != 0 {
		t.Fatal(code)
	}
	if got != "echo from-action" {
		t.Fatalf("cmd %q", got)
	}
}

func TestCopyNLimits(t *testing.T) {
	env, _, errb := testEnv(t)
	env.LoadHistory = func(context.Context, int) []histItem { return samplePairs() }
	if code := runCopy(context.Background(), env, []string{"answer", "6"}); code != 2 {
		t.Fatalf("want 2 got %d", code)
	}
	if !strings.Contains(errb.String(), "at most 5") {
		t.Fatalf("%q", errb.String())
	}
	errb.Reset()
	if code := runCopy(context.Background(), env, []string{"code", "6"}); code != 2 {
		t.Fatalf("code n %d", code)
	}
	if code := runCopy(context.Background(), env, nil); code != 2 {
		t.Fatalf("usage %d", code)
	}
}

func TestCopyFromLocalFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	env, out, _ := testEnv(t)
	var got string
	env.Copy = func(_ context.Context, text string) (string, error) {
		got = text
		return "osc52", nil
	}
	cwd := env.Cwd
	if err := memory.SaveLast(memory.LastSession{
		Profile: "default",
		CWD:     cwd,
		Title:   "local",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "hi"},
			{Role: llm.RoleAssistant, Content: "hello **there**"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if code := runCopy(context.Background(), env, []string{"answer"}); code != 0 {
		t.Fatalf("code %d", code)
	}
	if got != "hello **there**" {
		t.Fatalf("%q", got)
	}
	if !strings.Contains(out.String(), "via osc52") {
		t.Fatalf("%q", out.String())
	}
}

func TestCopyCmdFallsBackToBashBlock(t *testing.T) {
	env, _, _ := testEnv(t)
	var got string
	env.Copy = func(_ context.Context, text string) (string, error) {
		got = text
		return "fake", nil
	}
	env.LoadHistory = func(context.Context, int) []histItem {
		return []histItem{{
			Msgs: []llm.Message{
				{Role: llm.RoleUser, Content: "q"},
				{Role: llm.RoleAssistant, Content: "```bash\necho hi\n```\n"},
			},
		}}
	}
	if code := runCopy(context.Background(), env, []string{"cmd"}); code != 0 {
		t.Fatal(code)
	}
	if got != "echo hi" {
		t.Fatalf("%q", got)
	}
}

func TestHistoryShowDiffResumeNew(t *testing.T) {
	env, out, errb := testEnv(t)
	env.LoadHistory = func(_ context.Context, limit int) []histItem {
		items := samplePairs()
		if limit < len(items) {
			return items[:limit]
		}
		return items
	}
	if code := runHistory(context.Background(), env, nil); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(out.String(), "1  ") || !strings.Contains(out.String(), "one-liner") {
		t.Fatalf("hist %q", out.String())
	}
	out.Reset()
	if code := runShow(context.Background(), env, []string{"1"}); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(out.String(), "biggest files?") {
		t.Fatalf("show %q", out.String())
	}
	if code := runShow(context.Background(), env, nil); code != 2 {
		t.Fatalf("show usage %d", code)
	}
	out.Reset()
	if code := runDiff(context.Background(), env, nil); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(out.String(), "a.txt") {
		t.Fatalf("diff %q", out.String())
	}

	var gotReq turn.Request
	env.RunTurn = func(_ context.Context, req turn.Request) turn.Result {
		gotReq = req
		return turn.Result{ExitCode: 0, SessionID: "s1"}
	}
	if code := runResume(context.Background(), env, []string{"1", "and then?"}); code != 0 {
		t.Fatal(code)
	}
	if gotReq.ResumeSession != "s1" || gotReq.Text != "and then?" {
		t.Fatalf("%+v", gotReq)
	}
	if code := runResume(context.Background(), env, []string{"1"}); code != 2 {
		t.Fatalf("resume usage %d %s", code, errb.String())
	}

	closed := false
	env.CloseAll = func(context.Context) error { closed = true; return nil }
	if code := runNew(context.Background(), env, nil); code != 0 {
		t.Fatal(code)
	}
	if !closed {
		t.Fatal("CloseAll not called")
	}
}

func TestHistoryNLimit(t *testing.T) {
	env, _, errb := testEnv(t)
	if code := runHistory(context.Background(), env, []string{"51"}); code != 2 {
		t.Fatalf("%d", code)
	}
	if !strings.Contains(errb.String(), "at most 50") {
		t.Fatalf("%q", errb.String())
	}
}

func TestDispatchUnknownAndHelp(t *testing.T) {
	env, out, errb := testEnv(t)
	reg := Default()
	if code := reg.Dispatch(context.Background(), env, "hsitory", nil); code != 2 {
		t.Fatalf("%d", code)
	}
	if !strings.Contains(errb.String(), "unknown command /hsitory") || !strings.Contains(errb.String(), "did you mean /history") {
		t.Fatalf("%q", errb.String())
	}
	if code := runHelp(context.Background(), env, nil); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(out.String(), "/help") || !strings.Contains(out.String(), "/status") || !strings.Contains(out.String(), "/copy") {
		t.Fatalf("help %q", out.String())
	}
	if !strings.Contains(out.String(), "/exit, /quit") {
		t.Fatalf("help aliases %q", out.String())
	}
	if strings.Contains(out.String(), "--") {
		t.Fatalf("help still uses -- %q", out.String())
	}
	if Default().Lookup("quit") == nil || Default().Lookup("quit").Name != "exit" {
		t.Fatal("quit should alias exit")
	}
}

func TestSlashLines(t *testing.T) {
	s := Default().SlashLines()
	for _, want := range []string{"/help:", "/output:", "/status:", "/think:", "/cmd:"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in %q", want, s)
		}
	}
}

func TestStatus(t *testing.T) {
	env, out, _ := testEnv(t)
	if code := runStatus(context.Background(), env, nil); code != 0 {
		t.Fatal(code)
	}
	s := out.String()
	for _, want := range []string{"version", "test", "profile", "endpoint", "model", "fast_model", "routing", "database", "cwd", "repo", "clipboard", "build_thinking"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in %q", want, s)
		}
	}
	if strings.Contains(s, "postgres://") || strings.Contains(s, "PASSWORD") {
		t.Fatalf("credentials %q", s)
	}
}

func TestStatusWindowFromModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "main-model", "max_model_len": 94208}},
		})
	}))
	t.Cleanup(srv.Close)
	env, out, _ := testEnv(t)
	env.Profile.SetMain(srv.URL+"/v1", "main-model", false)
	client := llm.New(srv.URL+"/v1", "main-model")
	if client.ContextWindow() != 32768 {
		t.Fatalf("fallback %d", client.ContextWindow())
	}
	env.Runner = &turn.Runner{Client: client, Profile: env.Profile, Out: io.Discard, Err: io.Discard}
	if code := runStatus(context.Background(), env, nil); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(out.String(), "context        94208") {
		t.Fatalf("want server max_model_len, got %q", out.String())
	}
	if client.ContextWindow() != 94208 {
		t.Fatalf("client window %d", client.ContextWindow())
	}
}

func TestThinkCommand(t *testing.T) {
	env, out, errb := testEnv(t)
	env.InRepl = true
	env.Runner = &turn.Runner{}
	if code := Default().Dispatch(context.Background(), env, "think", []string{"on"}); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	if env.Runner.ThinkMode != turn.ThinkAlways {
		t.Fatalf("mode %v", env.Runner.ThinkMode)
	}
	if !strings.Contains(out.String(), "think on") {
		t.Fatalf("%q", out.String())
	}
	if code := Default().Dispatch(context.Background(), env, "think", []string{"nope"}); code != 2 {
		t.Fatalf("bad %d", code)
	}
}

func TestReplOnlyRejectedInOneShot(t *testing.T) {
	env, _, errb := testEnv(t)
	if code := Default().Dispatch(context.Background(), env, "exit", nil); code != 2 {
		t.Fatalf("%d", code)
	}
	if !strings.Contains(errb.String(), "only available in the REPL") {
		t.Fatalf("%q", errb.String())
	}
}

func TestManualCompact(t *testing.T) {
	env, out, errb := testEnv(t)
	if code := Default().Dispatch(context.Background(), env, "compact", nil); code != 2 {
		t.Fatalf("one-shot %d %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "only available in the REPL") {
		t.Fatalf("%q", errb.String())
	}
	errb.Reset()
	env.InRepl = true
	env.OnCompact = func(context.Context) int {
		fmt.Fprintln(env.Out, "compacted 6 messages (ctx 74% -> 18%)")
		return 0
	}
	if code := Default().Dispatch(context.Background(), env, "compact", nil); code != 0 {
		t.Fatalf("repl compact %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "compacted 6 messages") {
		t.Fatalf("%q", out.String())
	}
}

func TestRememberFactsForgetPruneNoStore(t *testing.T) {
	env, _, errb := testEnv(t)
	env.Mem = memory.NewGuard(nil, nil)
	if code := runRemember(context.Background(), env, []string{"a fact"}); code != 1 {
		t.Fatalf("remember %d", code)
	}
	if !strings.Contains(errb.String(), "memory is not configured") {
		t.Fatalf("%q", errb.String())
	}
	if code := runFacts(context.Background(), env, nil); code != 1 {
		t.Fatal(code)
	}
	if code := runForget(context.Background(), env, []string{"user", "1"}); code != 1 {
		t.Fatal(code)
	}
	if code := runPrune(context.Background(), env, nil); code != 1 {
		t.Fatal(code)
	}
	if code := runRemember(context.Background(), env, nil); code != 2 {
		t.Fatal(code)
	}
	if code := runForget(context.Background(), env, []string{"user"}); code != 2 {
		t.Fatal(code)
	}
}

type fakeConf struct{ d ui.Decision }

func (f fakeConf) Confirm(string, string) (ui.Decision, string, error) {
	return f.d, "", nil
}
func (f fakeConf) ConfirmForced(string, string) (ui.Decision, string, error) {
	return f.d, "", nil
}

func TestRunBlockSelectionAndGuard(t *testing.T) {
	env, out, errb := testEnv(t)
	env.LoadHistory = func(context.Context, int) []histItem {
		return []histItem{{
			Msgs: []llm.Message{
				{Role: llm.RoleUser, Content: "q"},
				{Role: llm.RoleAssistant, Content: "```bash\necho one\n```\n```sh\necho two\n```\n"},
			},
		}}
	}
	if code := runRun(context.Background(), env, nil); code != 2 {
		t.Fatalf("need k %d", code)
	}
	if !strings.Contains(out.String(), "1  ") || !strings.Contains(out.String(), "2  ") {
		t.Fatalf("list %q", out.String())
	}
	if !strings.Contains(errb.String(), "usage: /run") {
		t.Fatalf("%q", errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := runRun(context.Background(), env, []string{"1"}); code != 0 {
		t.Fatalf("run 1 %d %s %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "echo one") {
		t.Fatalf("cmd %q", out.String())
	}

	env.LoadHistory = func(context.Context, int) []histItem {
		return []histItem{{
			Msgs: []llm.Message{
				{Role: llm.RoleUser, Content: "q"},
				{Role: llm.RoleAssistant, Content: "```bash\nsudo rm -rf /\n```\n"},
			},
		}}
	}
	out.Reset()
	errb.Reset()
	if code := runRun(context.Background(), env, nil); code != 1 {
		t.Fatalf("guard %d %s %s", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "denied") && !strings.Contains(errb.String(), "sudo") {
		t.Fatalf("guard err %q", errb.String())
	}

	env.LoadHistory = func(context.Context, int) []histItem {
		return []histItem{{
			Msgs: []llm.Message{
				{Role: llm.RoleUser, Content: "q"},
				{Role: llm.RoleAssistant, Content: "```bash\ntouch newfile\n```\n"},
			},
		}}
	}
	env.Confirm = fakeConf{d: ui.Reject}
	if code := runRun(context.Background(), env, nil); code != 3 {
		t.Fatalf("reject %d", code)
	}
}

func TestNewClearsLocalFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	env, _, _ := testEnv(t)
	if err := memory.SaveLast(memory.LastSession{
		Profile: "default", CWD: env.Cwd, Title: "t",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := memory.LoadLast("default", env.Cwd, 0); !ok {
		t.Fatal("want last")
	}
	if code := runNew(context.Background(), env, nil); code != 0 {
		t.Fatal(code)
	}
	if _, ok := memory.LoadLast("default", env.Cwd, 0); ok {
		t.Fatal("fallback should be cleared")
	}
}

func TestOutputFromRunner(t *testing.T) {
	env, out, errb := testEnv(t)
	env.Runner = &turn.Runner{}
	env.Runner.RememberShell("first\n")
	env.Runner.RememberShell("second\n")
	if code := runOutput(context.Background(), env, nil); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	if out.String() != "second\n" {
		t.Fatalf("last %q", out.String())
	}
	out.Reset()
	if code := runOutput(context.Background(), env, []string{"2"}); code != 0 {
		t.Fatal(code)
	}
	if out.String() != "first\n" {
		t.Fatalf("n=2 %q", out.String())
	}
	if code := runOutput(context.Background(), env, []string{"9"}); code != 2 {
		t.Fatalf("want usage %d", code)
	}
}

func TestOutputMissing(t *testing.T) {
	env, _, errb := testEnv(t)
	if code := runOutput(context.Background(), env, nil); code != 1 {
		t.Fatalf("%d", code)
	}
	if !strings.Contains(errb.String(), "no shell output") {
		t.Fatalf("%q", errb.String())
	}
}

func TestRunCmd(t *testing.T) {
	env, out, errb := testEnv(t)
	if code := runCmd(context.Background(), env, nil); code != 2 {
		t.Fatalf("no callback %d", code)
	}
	if !strings.Contains(errb.String(), "/cmd is only available") {
		t.Fatalf("%q", errb.String())
	}
	toggled := false
	env.OnCmd = func() int { toggled = true; return 0 }
	if code := runCmd(context.Background(), env, nil); code != 0 || !toggled {
		t.Fatal("toggle")
	}
	forgot := false
	env.OnCmdClear = func() { forgot = true }
	if code := runCmd(context.Background(), env, []string{"forget"}); code != 0 || !forgot {
		t.Fatal("forget")
	}
	screen := false
	env.OnClear = func() { screen = true }
	if code := runCmd(context.Background(), env, []string{"clear"}); code != 0 || !screen || !strings.Contains(out.String(), "\x1b[3J") {
		t.Fatal("clear")
	}
	if code := runCmd(context.Background(), env, []string{"nope"}); code != 2 {
		t.Fatalf("usage %d", code)
	}
}

func TestConfigPathAndShowMask(t *testing.T) {
	env, out, errb := testEnv(t)
	env.ConfigPath = "/tmp/xdg/konsolai/config.yaml"
	env.Profile = config.Builtin()
	env.Profile.Name = "home"
	env.Profile.Database.URL = "postgres://user:hunter2@localhost:5432/k"
	if code := runConfig(context.Background(), env, []string{"path"}); code != 0 {
		t.Fatalf("path %d %s", code, errb.String())
	}
	wantPath := env.ConfigPath + "\n" + env.ConfigPath + "\n"
	if out.String() != wantPath {
		t.Fatalf("path %q", out.String())
	}
	out.Reset()
	if code := runConfig(context.Background(), env, []string{"show"}); code != 0 {
		t.Fatalf("show %d %s", code, errb.String())
	}
	got := out.String()
	if strings.Contains(got, "hunter2") {
		t.Fatalf("leaked %s", got)
	}
	if !strings.Contains(got, "***") || !strings.Contains(got, "profile: home") {
		t.Fatalf("show %s", got)
	}
}

func TestConfigValidateMissingIsOK(t *testing.T) {
	env, out, errb := testEnv(t)
	env.ConfigPath = filepath.Join(t.TempDir(), "missing.yaml")
	if code := runConfig(context.Background(), env, []string{"validate"}); code != 0 {
		t.Fatalf("validate %d %s", code, errb.String())
	}
	if strings.TrimSpace(out.String()) != "ok" {
		t.Fatalf("got %q", out.String())
	}
}

func TestRunClearWipesScrollback(t *testing.T) {
	env, out, _ := testEnv(t)
	env.InRepl = true
	called := false
	env.OnClear = func() { called = true }
	if code := runClear(context.Background(), env, nil); code != 0 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(out.String(), "\x1b[3J") || !strings.Contains(out.String(), "\x1b[H\x1b[2J") {
		t.Fatalf("missing wipe %q", out.String())
	}
	if !called {
		t.Fatal("OnClear not called")
	}
}

func TestHelpAlignsSummaries(t *testing.T) {
	env, out, _ := testEnv(t)
	if code := runHelp(context.Background(), env, nil); code != 0 {
		t.Fatal(code)
	}
	cmds := Default().All()
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != len(cmds) {
		t.Fatalf("%d lines for %d commands", len(lines), len(cmds))
	}
	col := -1
	for i, line := range lines {
		at := strings.Index(line, cmds[i].Summary)
		if at < 0 {
			t.Fatalf("missing summary in %q", line)
		}
		if col >= 0 && at != col {
			t.Fatalf("summary at %d, want %d: %q", at, col, line)
		}
		col = at
	}
	if !strings.Contains(out.String(), "/resume <n> <request>") || !strings.Contains(out.String(), "/history [n] [here]") {
		t.Fatalf("args missing:\n%s", out.String())
	}

	out.Reset()
	env.InRepl = true
	runHelp(context.Background(), env, nil)
	if !strings.Contains(out.String(), "? on a row shows details") {
		t.Fatalf("repl tip missing:\n%s", out.String())
	}
}
