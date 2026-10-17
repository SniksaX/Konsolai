package store

import (
	"context"
	"testing"
	"time"
)

func TestTurnStatRateAndZeroMillis(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id, err := s.CreateSession(ctx, NewSession{
		Profile: "home", CWD: "/tmp/stats", Mode: "answer", Model: "qwen", Title: "t",
	})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour)
	rows := []struct {
		model string
		comp  int
		ms    int
		at    time.Time
	}{
		{"qwen", 100, 0, base},
		{"qwen", 100, 1000, base.Add(time.Minute)},
		{"qwen", 50, 500, base.Add(2 * time.Minute)},
		{"other", 999, 1000, base.Add(3 * time.Minute)},
	}
	for _, r := range rows {
		if err := s.RecordTurnStat(ctx, TurnStat{
			SessionID: id, Intent: "answer", Model: r.model, Endpoint: "http://fatboy/v1",
			PromptTokens: 10, CompletionTokens: r.comp, ModelMS: r.ms,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `
			UPDATE turn_stats SET created_at = $1
			WHERE id = (SELECT MAX(id) FROM turn_stats)`, r.at); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.ModelRate(ctx, "qwen", 20)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("want rate")
	}
	// 250 tokens / 1.5s. The zero-ms row stays in the window and must not panic.
	if *got < 166 || *got > 167 {
		t.Fatalf("rate %v", *got)
	}

	latest, err := s.ModelRate(ctx, "qwen", 1)
	if err != nil || latest == nil {
		t.Fatalf("latest %v err=%v", latest, err)
	}
	if *latest < 99 || *latest > 101 {
		t.Fatalf("latest rate %v", *latest)
	}

	none, err := s.ModelRate(ctx, "missing", 20)
	if err != nil || none != nil {
		t.Fatalf("missing rate %v err=%v", none, err)
	}

	if _, err := s.pool.Exec(ctx, `DELETE FROM turn_stats WHERE model = 'qwen' AND model_ms > 0`); err != nil {
		t.Fatal(err)
	}
	zero, err := s.ModelRate(ctx, "qwen", 20)
	if err != nil {
		t.Fatal(err)
	}
	if zero != nil {
		t.Fatalf("zero ms rate %v", *zero)
	}

	if _, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1::uuid`, id); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM turn_stats WHERE session_id = $1::uuid`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("cascade left %d", n)
	}
}

func TestRecordTurnStatNullSession(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.RecordTurnStat(ctx, TurnStat{
		Intent: "answer", Model: "fast", Endpoint: "http://fatboy:11435/v1",
		PromptTokens: 3, CompletionTokens: 4, ModelMS: 100,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ModelRate(ctx, "fast", 20)
	if err != nil || got == nil || *got < 39 || *got > 41 {
		t.Fatalf("rate %v err=%v", got, err)
	}
	def, err := s.ModelRate(ctx, "fast", 0)
	if err != nil || def == nil || *def != *got {
		t.Fatalf("default lastN %v want %v err=%v", def, got, err)
	}
}
