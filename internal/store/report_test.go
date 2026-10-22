package store

import (
	"context"
	"testing"
	"time"
)

func TestUsageReportGroupsWindow(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id, err := s.CreateSession(ctx, NewSession{
		Profile: "home", CWD: "/tmp/stats-report", Mode: "answer", Model: "main-model", Title: "t",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	rows := []struct {
		model, intent string
		prompt, comp  int
		ms            int
		at            time.Time
	}{
		{"main-model", "answer", 10, 100, 1000, now.Add(-time.Hour)},
		{"main-model", "answer", 10, 50, 500, now.Add(-30 * time.Minute)},
		{"other", "lookup", 5, 10, 1000, now.Add(-10 * time.Minute)},
		{"old", "answer", 9, 9, 100, now.Add(-48 * time.Hour)},
	}
	for _, r := range rows {
		if err := s.RecordTurnStat(ctx, TurnStat{
			SessionID: id, Intent: r.intent, Model: r.model, Endpoint: "http://example/v1",
			PromptTokens: r.prompt, CompletionTokens: r.comp, ModelMS: r.ms,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `
			UPDATE turn_stats SET created_at = $1
			WHERE id = (SELECT MAX(id) FROM turn_stats)`, r.at); err != nil {
			t.Fatal(err)
		}
	}
	since := now.Add(-24 * time.Hour)
	got, err := s.UsageReport(ctx, &since)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 2 {
		t.Fatalf("models %+v", got.Models)
	}
	main := got.Models[0]
	if main.Model != "main-model" || main.Calls != 2 || main.PromptTokens != 20 || main.CompletionTokens != 150 {
		t.Fatalf("main %+v", main)
	}
	if main.TokPerSec == nil || *main.TokPerSec < 99.9 || *main.TokPerSec > 100.1 {
		t.Fatalf("tok/s %v", main.TokPerSec)
	}
	if main.P50MS < 749 || main.P50MS > 751 {
		t.Fatalf("p50 %v", main.P50MS)
	}
	if main.P95MS < 974 || main.P95MS > 976 {
		t.Fatalf("p95 %v", main.P95MS)
	}
	if len(got.Intents) != 2 || got.Intents[0].Intent != "answer" || got.Intents[0].Turns != 2 {
		t.Fatalf("intents %+v", got.Intents)
	}
	if got.Intents[0].AvgSteps < 1.9 || got.Intents[0].AvgSteps > 2.1 {
		t.Fatalf("steps %v", got.Intents[0].AvgSteps)
	}
	if got.Intents[0].AvgWallMS < 749 || got.Intents[0].AvgWallMS > 751 {
		t.Fatalf("wall %v", got.Intents[0].AvgWallMS)
	}
	if got.Totals.Turns != 3 || got.Totals.Tokens != 185 || got.Totals.Sessions != 1 {
		t.Fatalf("totals %+v", got.Totals)
	}
	all, err := s.UsageReport(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if all.Totals.Turns != 4 || all.Totals.Tokens != 203 {
		t.Fatalf("all %+v", all.Totals)
	}
}

func TestSchemaProbeCurrent(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	latest, err := LatestVersion()
	if err != nil {
		t.Fatal(err)
	}
	if latest != 9 {
		t.Fatalf("latest %d", latest)
	}
	got, err := s.SchemaVersion(ctx)
	if err != nil || got != latest {
		t.Fatalf("schema %d err=%v", got, err)
	}
	ok, err := s.HasVector(ctx)
	if err != nil || !ok {
		t.Fatalf("vector %v err=%v", ok, err)
	}
}
