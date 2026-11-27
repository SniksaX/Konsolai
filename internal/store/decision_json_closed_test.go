package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func newTestSession(t *testing.T, s *Store) string {
	t.Helper()
	ctx := context.Background()
	id, err := s.CreateSession(ctx, NewSession{Profile: "home", CWD: "/tmp/b2", Mode: "do", Model: "m", Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestActionDecisionCheckIncludesCodeRunningValues(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id := newTestSession(t, s)
	decisions := []string{
		"approved", "approved_all", "auto", "rejected", "gate_blocked", "invalid",
		"code_running_approved", "code_running_rejected", "code_running_refused",
	}
	for i, d := range decisions {
		if err := s.RecordAction(ctx, ActionIn{SessionID: id, Seq: i + 1, Tool: "write_file", Args: []byte(`{"path":"a"}`), Kind: "mutating", Decision: d}); err != nil {
			t.Fatalf("decision %q: %v", d, err)
		}
	}
	if err := s.RecordAction(ctx, ActionIn{SessionID: id, Seq: 99, Tool: "write_file", Args: []byte(`{}`), Kind: "mutating", Decision: "bogus"}); err == nil {
		t.Fatal("an unknown decision must still fail the check")
	}
}

func TestDecisionMigrationUpgradesAPopulatedPriorSchema(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id := newTestSession(t, s)
	if err := s.RecordAction(ctx, ActionIn{SessionID: id, Seq: 1, Tool: "write_file", Args: []byte(`{}`), Kind: "mutating", Decision: "approved"}); err != nil {
		t.Fatal(err)
	}
	// Rewind to the 0009 schema with one populated action row.
	if _, err := s.pool.Exec(ctx, "DELETE FROM schema_migrations WHERE version = 10"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `ALTER TABLE actions DROP CONSTRAINT IF EXISTS actions_decision_check`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `ALTER TABLE actions ADD CONSTRAINT actions_decision_check CHECK (decision IN ('approved','approved_all','auto','rejected','gate_blocked','invalid'))`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatalf("migrate over populated prior schema: %v", err)
	}
	if err := s.RecordAction(ctx, ActionIn{SessionID: id, Seq: 2, Tool: "write_file", Args: []byte(`{}`), Kind: "mutating", Decision: "code_running_refused"}); err != nil {
		t.Fatalf("new decision after migration: %v", err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM actions WHERE session_id = $1::uuid", id).Scan(&n); err != nil || n != 2 {
		t.Fatalf("old/new rows n=%d err=%v", n, err)
	}
}

func TestRecordActionStoresInvalidArgsAsValidJSON(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id := newTestSession(t, s)
	for i, raw := range []string{`not json at all`, `{"path":`, "runs code later: scripts/x.sh"} {
		if err := s.RecordAction(ctx, ActionIn{SessionID: id, Seq: i + 1, Tool: "write_file", Args: []byte(raw), Kind: "mutating", Decision: "code_running_refused"}); err != nil {
			t.Fatalf("raw %q: %v", raw, err)
		}
	}
	var valid bool
	if err := s.pool.QueryRow(ctx, "SELECT bool_and(jsonb_typeof(args) = 'string') FROM actions WHERE session_id = $1::uuid", id).Scan(&valid); err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Fatal("invalid tool args must be stored as a JSON string")
	}
	var got map[string]json.RawMessage
	if err := s.pool.QueryRow(ctx, "SELECT args FROM actions WHERE session_id = $1::uuid ORDER BY seq LIMIT 1", id).Scan(&got); err == nil {
		t.Fatal("a string payload must not decode as an object")
	}
}

func TestClosedStoreIsQuerySafe(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	s.Close()
	if s.RAGAvailable(ctx) {
		t.Fatal("a closed store cannot have RAG available")
	}
	if _, err := s.Meta(ctx, "rag"); err == nil {
		t.Fatal("Meta on a closed store must return an error")
	}
	s.Close() // idempotent
	_ = os.Getenv("KONSOLAI_TEST_DATABASE_URL")
	_ = time.Second
}
