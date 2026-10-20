package store

import (
	"context"
	"testing"
	"time"
)

func TestWelcomeMemoryAndRecent(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	pid, err := s.UpsertProject(ctx, "/tmp/welcome", []string{"go"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddUserFact(ctx, "home", "likes tea", "explicit"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddProjectFact(ctx, pid, "uses go", nil); err != nil {
		t.Fatal(err)
	}
	localNew, err := s.CreateSession(ctx, NewSession{
		Profile: "home", ProjectID: &pid, CWD: "/tmp/welcome", Mode: "answer", Model: "main-model", Title: "local new",
	})
	if err != nil {
		t.Fatal(err)
	}
	localOld, err := s.CreateSession(ctx, NewSession{
		Profile: "home", CWD: "/tmp/welcome", Mode: "answer", Model: "main-model", Title: "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(ctx, localOld, 1, Message{Role: "user", Content: "first prompt from the user"}); err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateSession(ctx, NewSession{
		Profile: "home", CWD: "/tmp/other", Mode: "act", Model: "main-model", Title: "elsewhere",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	stamp := func(id string, at time.Time) {
		t.Helper()
		if _, err := s.pool.Exec(ctx, `UPDATE sessions SET last_activity_at = $2 WHERE id = $1::uuid`, id, at); err != nil {
			t.Fatal(err)
		}
	}
	stamp(localOld, now.Add(-2*time.Hour))
	stamp(other, now.Add(-time.Hour))
	stamp(localNew, now.Add(-time.Minute))
	if err := s.RecordTurnStat(ctx, TurnStat{
		SessionID: localNew, Intent: "lookup", Model: "main-model", Endpoint: "http://gpu-box/v1",
		PromptTokens: 1, CompletionTokens: 1, ModelMS: 10,
	}); err != nil {
		t.Fatal(err)
	}

	facts, sessions, err := s.WelcomeMemory(ctx, "home", &pid)
	if err != nil {
		t.Fatal(err)
	}
	if facts != 2 || sessions != 3 {
		t.Fatalf("facts %d sessions %d", facts, sessions)
	}

	display, latest, ok, err := s.WelcomeRecent(ctx, "home", nil, "/tmp/welcome", 4)
	if err != nil || !ok {
		t.Fatalf("recent ok=%v err=%v", ok, err)
	}
	if latest.Title != "local new" || latest.Intent != "lookup" {
		t.Fatalf("latest %+v", latest)
	}
	if len(display) != 3 {
		t.Fatalf("len %d", len(display))
	}
	if display[0].Title != "local new" || display[1].CWD != "/tmp/welcome" || display[2].Title != "elsewhere" {
		t.Fatalf("%+v", display)
	}
	if display[1].Title != "" || display[1].FirstUser != "first prompt from the user" {
		t.Fatalf("fallback %+v", display[1])
	}
	if display[0].Intent != "lookup" || display[2].Intent != "act" {
		t.Fatalf("intents %+v %+v", display[0], display[2])
	}
	if !display[0].Here || !display[1].Here || display[2].Here {
		t.Fatalf("here %+v", display)
	}

	listed, err := s.NumberedSessions(ctx, "home", nil, "/tmp/welcome", 4, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != len(display) {
		t.Fatalf("list %d welcome %d", len(listed), len(display))
	}
	for i := range display {
		if listed[i].Title != display[i].Title || listed[i].CWD != display[i].CWD {
			t.Fatalf("number %d list %+v welcome %+v", i+1, listed[i], display[i])
		}
	}

	empty, err := s.UpsertProject(ctx, "/tmp/empty-project", []string{"go"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	global, _, _, err := s.WelcomeRecent(ctx, "home", &empty, "/tmp/empty-project", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(global) != 3 || global[0].Title != "local new" || global[0].Here {
		t.Fatalf("empty project %+v", global)
	}
	only, err := s.NumberedSessions(ctx, "home", &empty, "/tmp/empty-project", 4, true)
	if err != nil || len(only) != 0 {
		t.Fatalf("here only %d err=%v", len(only), err)
	}
}
