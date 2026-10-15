package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	u := os.Getenv("KONSOLAI_TEST_DATABASE_URL")
	if u == "" {
		t.Skip("KONSOLAI_TEST_DATABASE_URL not set")
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	schema := "kt_" + hex.EncodeToString(b[:])
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+ident+" CASCADE")
		admin.Close()
	})

	cfg, err := pgxpool.ParseConfig(u)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.ConnectTimeout = queryTimeout
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET search_path TO "+ident)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestMigrateIdempotent(t *testing.T) {
	s := openTest(t)
	if err := s.migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSessionResumeWindow(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	pid, err := s.UpsertProject(ctx, "/tmp/p", []string{"go"}, []string{"go test"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateSession(ctx, NewSession{
		Profile: "home", ProjectID: &pid, CWD: "/tmp/p", Mode: "do", Model: "m", Title: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.FindResumableSession(ctx, "home", &pid, "/tmp/p", "do", 30*time.Minute)
	if err != nil || !ok || got.ID != id {
		t.Fatalf("active resume: ok=%v id=%s err=%v", ok, got.ID, err)
	}
	if err := s.EndSession(ctx, id, "finished"); err != nil {
		t.Fatal(err)
	}
	got, ok, err = s.FindResumableSession(ctx, "home", &pid, "/tmp/p", "do", 30*time.Minute)
	if err != nil || !ok || got.ID != id {
		t.Fatalf("finished resume: ok=%v id=%s err=%v", ok, got.ID, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE sessions SET last_activity_at = now() - interval '2 hours' WHERE id = $1::uuid`, id); err != nil {
		t.Fatal(err)
	}
	_, ok, err = s.FindResumableSession(ctx, "home", &pid, "/tmp/p", "do", 30*time.Minute)
	if err != nil || ok {
		t.Fatalf("stale should miss: ok=%v err=%v", ok, err)
	}
}

func TestResumeModeFamily(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	doID, err := s.CreateSession(ctx, NewSession{
		Profile: "home", CWD: "/tmp/p", Mode: "do", Model: "m", Title: "do-title",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EndSession(ctx, doID, "finished"); err != nil {
		t.Fatal(err)
	}
	codeID, err := s.CreateSession(ctx, NewSession{
		Profile: "home", CWD: "/tmp/p", Mode: "code", Model: "m", Title: "code-title",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EndSession(ctx, codeID, "finished"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.FindResumableSession(ctx, "home", nil, "/tmp/p", "answer", 30*time.Minute)
	if err != nil || !ok || got.ID != codeID {
		t.Fatalf("resume ignores mode: ok=%v id=%s want=%s err=%v", ok, got.ID, codeID, err)
	}
	_ = doID
}

func TestResumeAppendsNoNewRow(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id, err := s.CreateSession(ctx, NewSession{
		Profile: "home", CWD: "/tmp/p", Mode: "do", Model: "m", Title: "first request",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(ctx, id, 1, Message{Role: "user", Content: "first request"}); err != nil {
		t.Fatal(err)
	}
	if err := s.EndSession(ctx, id, "finished"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.FindResumableSession(ctx, "home", nil, "/tmp/p", "do", 30*time.Minute)
	if err != nil || !ok || got.ID != id || got.Title != "first request" {
		t.Fatalf("resume %+v ok=%v err=%v", got, ok, err)
	}
	if err := s.AppendMessage(ctx, got.ID, 2, Message{Role: "user", Content: "follow up"}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE cwd = $1`, "/tmp/p").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("sessions %d want 1", n)
	}
	msgs, err := s.LoadMessages(ctx, id)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("msgs %d err=%v", len(msgs), err)
	}
}

func TestSeqOrderingAndRedaction(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id, err := s.CreateSession(ctx, NewSession{
		Profile: "home", CWD: "/tmp/x", Mode: "do", Model: "m", Title: "t",
	})
	if err != nil {
		t.Fatal(err)
	}
	secret := "AKIAIOSFODNN7EXAMPLE"
	for i, role := range []string{"user", "assistant", "tool"} {
		seq, err := s.NextSeq(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if seq != i+1 {
			t.Fatalf("seq %d want %d", seq, i+1)
		}
		if err := s.AppendMessage(ctx, id, seq, Message{Role: role, Content: "msg " + secret}); err != nil {
			t.Fatal(err)
		}
	}
	msgs, err := s.LoadMessages(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 || msgs[0].Role != "user" || msgs[2].Role != "tool" {
		t.Fatalf("order %+v", msgs)
	}
	for _, m := range msgs {
		if strings.Contains(m.Content, secret) {
			t.Fatalf("secret stored: %q", m.Content)
		}
		if !strings.Contains(m.Content, "[REDACTED:aws]") {
			t.Fatalf("want redaction: %q", m.Content)
		}
	}
	seq, err := s.NextSeq(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAction(ctx, ActionIn{
		SessionID: id, Seq: seq, Tool: "run_shell",
		Args: []byte(`{"cmd":"echo","token":"sk-abcdefghijklmnopqrstuvwxyz123456"}`),
		Kind: "mutating", Decision: "approved", Output: "token=supersecret",
	}); err != nil {
		t.Fatal(err)
	}
	var args, output string
	if err := s.pool.QueryRow(ctx, `SELECT args::text, output FROM actions WHERE session_id = $1::uuid`, id).Scan(&args, &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(args, "sk-abcdefghijklmnopqrst") || strings.Contains(output, "supersecret") {
		t.Fatalf("action not redacted args=%s out=%s", args, output)
	}
}

func TestSensitiveFileUndoBlocked(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id, err := s.CreateSession(ctx, NewSession{
		Profile: "home", CWD: "/tmp/x", Mode: "code", Model: "m", Title: "t",
	})
	if err != nil {
		t.Fatal(err)
	}
	seq, err := s.NextSeq(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordFileChange(ctx, FileChangeIn{
		SessionID: id, Seq: seq, Path: ".env", ExistedBefore: true,
		Before: []byte("SECRET=1"), AfterHash: "aaa", Diff: "-SECRET=1",
	}); err != nil {
		t.Fatal(err)
	}
	seq, err = s.NextSeq(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	big := make([]byte, maxBefore+1)
	if err := s.RecordFileChange(ctx, FileChangeIn{
		SessionID: id, Seq: seq, Path: "big.bin", ExistedBefore: true,
		Before: big, AfterHash: "bbb", Diff: "big",
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.UndoCandidates(ctx, nil, "/tmp/x")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows", len(rows))
	}
	byPath := map[string]FileChangeRow{}
	for _, r := range rows {
		byPath[r.Path] = r
	}
	if byPath[".env"].UndoBlocked != "sensitive file" || byPath[".env"].BeforeContent != nil {
		t.Fatalf(".env %+v", byPath[".env"])
	}
	if byPath["big.bin"].UndoBlocked != "too large" || byPath["big.bin"].BeforeContent != nil {
		t.Fatalf("big %+v", byPath["big.bin"])
	}
}

func TestPruneIfDue(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id, err := s.CreateSession(ctx, NewSession{
		Profile: "home", CWD: "/tmp/old", Mode: "do", Model: "m", Title: "old",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE sessions SET last_activity_at = now() - interval '200 days' WHERE id = $1::uuid`, id); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneIfDue(ctx, 90); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE id = $1::uuid`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("old session not pruned")
	}
	id2, err := s.CreateSession(ctx, NewSession{
		Profile: "home", CWD: "/tmp/old", Mode: "do", Model: "m", Title: "also-old",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE sessions SET last_activity_at = now() - interval '200 days' WHERE id = $1::uuid`, id2); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneIfDue(ctx, 90); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE id = $1::uuid`, id2).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("second prune within 24h should skip")
	}
}

func TestWebCacheExpiry(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.PutWebCache(ctx, "https://ex", "T", "hello", time.Hour); err != nil {
		t.Fatal(err)
	}
	title, text, ok, err := s.GetWebCache(ctx, "https://ex")
	if err != nil || !ok || title != "T" || text != "hello" {
		t.Fatalf("hit title=%q text=%q ok=%v err=%v", title, text, ok, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE web_cache SET expires_at = now() - interval '1 second' WHERE url = $1`, "https://ex"); err != nil {
		t.Fatal(err)
	}
	_, _, ok, err = s.GetWebCache(ctx, "https://ex")
	if err != nil || ok {
		t.Fatalf("expired should miss ok=%v err=%v", ok, err)
	}
}

func TestFacts(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	pid, err := s.UpsertProject(ctx, "/tmp/p", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	uid, err := s.AddUserFact(ctx, "home", "I use pnpm", "explicit")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddProjectFact(ctx, pid, "module demo", nil); err != nil {
		t.Fatal(err)
	}
	u, p, err := s.ListFacts(ctx, "home", &pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(u) != 1 || u[0].ID != uid || len(p) != 1 {
		t.Fatalf("user=%+v project=%+v", u, p)
	}
	if err := s.DeleteFact(ctx, "user", uid); err != nil {
		t.Fatal(err)
	}
	u, _, err = s.ListFacts(ctx, "home", &pid)
	if err != nil || len(u) != 0 {
		t.Fatalf("after delete user=%+v err=%v", u, err)
	}
}

func TestOpenRequiresPostgres13(t *testing.T) {
	if os.Getenv("KONSOLAI_TEST_DATABASE_URL") == "" {
		t.Skip("KONSOLAI_TEST_DATABASE_URL not set")
	}
	s := openTest(t)
	var ver int
	if err := s.pool.QueryRow(context.Background(), "SELECT current_setting('server_version_num')::int").Scan(&ver); err != nil {
		t.Fatal(err)
	}
	if ver < minServerVersion {
		t.Fatalf("unexpected version %d", ver)
	}
}
