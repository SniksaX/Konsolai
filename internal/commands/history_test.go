package commands

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/memory"
	"github.com/SniksaX/Konsolai/internal/store"
	"github.com/SniksaX/Konsolai/internal/turn"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
)

func TestHistoryListsOtherProjectsAndResumeMatches(t *testing.T) {
	raw := os.Getenv("KONSOLAI_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("KONSOLAI_TEST_DATABASE_URL not set")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	ctx := context.Background()
	st := openHistoryStore(t, raw)
	here := t.TempDir()
	other := t.TempDir()
	gitInit(t, here)
	gitInit(t, other)
	pidOther, err := st.UpsertProject(ctx, other, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSession(ctx, store.NewSession{
		Profile: "sesslist", ProjectID: &pidOther, CWD: other, Mode: "answer", Model: "m", Title: "older global",
	}); err != nil {
		t.Fatal(err)
	}
	newer, err := st.CreateSession(ctx, store.NewSession{
		Profile: "sesslist", ProjectID: &pidOther, CWD: other, Mode: "act", Model: "m", Title: "newer global",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.TouchSession(ctx, newer); err != nil {
		t.Fatal(err)
	}

	env, out, errb := testEnv(t)
	env.Profile = config.Profile{Name: "sesslist"}
	env.Cwd = here
	env.Mem = memory.NewGuard(st, func(error) {})
	env.InRepl = true
	if code := runHistory(ctx, env, nil); code != 0 {
		t.Fatalf("history %d %s", code, errb.String())
	}
	text := out.String()
	first := strings.Index(text, "newer global")
	second := strings.Index(text, "older global")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("order %q", text)
	}
	if !strings.HasPrefix(strings.TrimSpace(text), "1  ") || !strings.Contains(text, other) {
		t.Fatalf("numbered %q", text)
	}
	if strings.Contains(text, "no sessions") {
		t.Fatalf("empty %q", text)
	}

	pidHere, err := st.UpsertProject(ctx, here, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	display, _, ok, err := st.WelcomeRecent(ctx, "sesslist", &pidHere, here, 4)
	if err != nil || !ok || len(display) < 2 {
		t.Fatalf("recent ok=%v n=%d err=%v", ok, len(display), err)
	}
	if display[0].Title != "newer global" || display[1].Title != "older global" {
		t.Fatalf("recent %+v", display)
	}
	if display[0].Here || display[1].Here {
		t.Fatalf("here flags %+v", display)
	}

	out.Reset()
	if code := runHistory(ctx, env, []string{"--here"}); code != 0 {
		t.Fatalf("here %d %s", code, errb.String())
	}
	if strings.TrimSpace(out.String()) != "no sessions" {
		t.Fatalf("--here %q", out.String())
	}

	var got turn.Request
	env.RunTurn = func(_ context.Context, req turn.Request) turn.Result {
		got = req
		return turn.Result{ExitCode: 0}
	}
	if code := runResume(ctx, env, []string{"1", "continue"}); code != 0 {
		t.Fatalf("resume %d %s", code, errb.String())
	}
	if got.ResumeSession != newer || got.Text != "continue" {
		t.Fatalf("resumed %+v want %s", got, newer)
	}
}

func openHistoryStore(t *testing.T, raw string) *store.Store {
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
	schema := "kthist_" + hex.EncodeToString(tok[:])
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

func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-b", "main", dir)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
}

func TestHistoryColumnsAlign(t *testing.T) {
	env, out, _ := testEnv(t)
	env.LoadHistory = func(context.Context, int) []histItem {
		return []histItem{
			{Title: strings.Repeat("long title ", 6), Mode: "build", Status: "failed", CWD: "/p/one"},
			{Title: "short", Mode: "answer", Status: "finished", CWD: "/p/two"},
		}
	}
	if code := runHistory(context.Background(), env, nil); code != 0 {
		t.Fatal(code)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("%q", out.String())
	}
	one := theme.DisplayWidth(lines[0][:strings.Index(lines[0], "/p/one")])
	two := theme.DisplayWidth(lines[1][:strings.Index(lines[1], "/p/two")])
	if one != two {
		t.Fatalf("cwd columns at %d and %d:\n%s", one, two, out.String())
	}
}
