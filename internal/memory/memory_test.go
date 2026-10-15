package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/siraj/konsolai/internal/agent"
	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/store"
	"github.com/siraj/konsolai/internal/tools"
)

type fake struct {
	failNext bool
	msgs     int
	actions  int
	files    int
	puts     int
	facts    int
	closed   bool
	seq      int
}

func (f *fake) NextSeq(context.Context, string) (int, error) {
	if f.failNext {
		return 0, errors.New("boom")
	}
	f.seq++
	return f.seq, nil
}
func (f *fake) AppendMessage(context.Context, string, int, store.Message) error {
	if f.failNext {
		return errors.New("boom")
	}
	f.msgs++
	return nil
}
func (f *fake) TouchSession(context.Context, string) error { return nil }
func (f *fake) RecordAction(context.Context, store.ActionIn) error {
	f.actions++
	return nil
}
func (f *fake) RecordFileChange(context.Context, store.FileChangeIn) error {
	f.files++
	return nil
}
func (f *fake) GetWebCache(context.Context, string) (string, string, bool, error) {
	if f.failNext {
		return "", "", false, errors.New("boom")
	}
	return "", "", false, nil
}
func (f *fake) PutWebCache(context.Context, string, string, string, time.Duration) error {
	f.puts++
	return nil
}
func (f *fake) AddUserFact(context.Context, string, string, string) (int64, error) {
	if f.failNext {
		return 0, errors.New("boom")
	}
	f.facts++
	return 1, nil
}
func (f *fake) AddProjectFact(context.Context, int64, string, *string) (int64, error) {
	f.facts++
	return 2, nil
}
func (f *fake) Close() { f.closed = true }

func TestRecorderBestEffort(t *testing.T) {
	f := &fake{}
	var disabled int
	g := NewGuard(f, func(error) { disabled++ })
	rec := NewRecorder(g, "sess")
	if err := rec.Message(context.Background(), llm.Message{Role: llm.RoleUser, Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if f.msgs != 1 {
		t.Fatalf("msgs %d", f.msgs)
	}
	if err := rec.Action(context.Background(), agent.ActionRecord{Tool: "t", Decision: "auto"}); err != nil {
		t.Fatal(err)
	}
	if err := rec.FileChange(context.Background(), tools.FileChange{Path: "a.txt", AfterHash: "x"}); err != nil {
		t.Fatal(err)
	}
	f.failNext = true
	if err := rec.Message(context.Background(), llm.Message{Role: llm.RoleUser, Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if disabled != 1 {
		t.Fatalf("disabled %d", disabled)
	}
	if !f.closed {
		t.Fatal("want close on disable")
	}
	if err := rec.Message(context.Background(), llm.Message{Role: llm.RoleUser, Content: "y"}); err != nil {
		t.Fatal(err)
	}
	if disabled != 1 {
		t.Fatal("disable once")
	}
}

func TestWebCacheAndFactsBestEffort(t *testing.T) {
	f := &fake{}
	g := NewGuard(f, nil)
	c := NewWebCache(g)
	if _, _, ok := c.Get(context.Background(), "https://x"); ok {
		t.Fatal("empty cache")
	}
	if err := c.Put(context.Background(), "https://x", "t", "b", time.Hour); err != nil {
		t.Fatal(err)
	}
	if f.puts != 1 {
		t.Fatalf("puts %d", f.puts)
	}
	fs := NewFactStore(g, "home", nil, "sess")
	if err := fs.Add(context.Background(), "I use pnpm", "user"); err != nil {
		t.Fatal(err)
	}
	if f.facts != 1 {
		t.Fatalf("facts %d", f.facts)
	}
	if err := fs.Add(context.Background(), "x", "project"); err == nil || !strings.Contains(err.Error(), "no project") {
		t.Fatalf("project without id: %v", err)
	}
	f.failNext = true
	if err := fs.Add(context.Background(), "y", "user"); err == nil {
		t.Fatal("want error")
	}
	if !g.Disabled() {
		t.Fatal("want disabled")
	}
	if err := fs.Add(context.Background(), "z", "user"); err != nil {
		t.Fatal(err)
	}
}

func TestToLLM(t *testing.T) {
	got := ToLLM([]store.Message{{Role: "user", Content: "hi", Seq: 1}})
	if len(got) != 1 || got[0].Role != llm.RoleUser || got[0].Content != "hi" {
		t.Fatalf("%+v", got)
	}
}

func TestNopGuard(t *testing.T) {
	g := NewGuard(nil, nil)
	if !g.Disabled() {
		t.Fatal("nil store is disabled")
	}
	rec := NewRecorder(g, "")
	if err := rec.Message(context.Background(), llm.Message{Content: "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestFailIgnoresCancel(t *testing.T) {
	f := &fake{}
	var n int
	g := NewGuard(f, func(error) { n++ })
	g.Fail(context.Canceled)
	if n != 0 || g.Disabled() {
		t.Fatalf("n=%d disabled=%v", n, g.Disabled())
	}
	g.Fail(fmt.Errorf("store: end: %w", context.Canceled))
	if n != 0 || g.Disabled() {
		t.Fatal("wrapped cancel")
	}
	f.failNext = true
	rec := NewRecorder(g, "s")
	_ = rec.Message(context.Background(), llm.Message{Content: "x"})
	if n != 1 {
		t.Fatalf("real error should disable, n=%d", n)
	}
}

func TestIsolatedSurvivesCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	iso, stop := Isolated(ctx)
	defer stop()
	if err := iso.Err(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-iso.Done():
		t.Fatal("isolated ctx already done")
	default:
	}
}

func TestTypedNilStoreDisabled(t *testing.T) {
	var s *store.Store
	g := NewGuard(s, nil)
	if !g.Disabled() {
		t.Fatal("typed nil *store.Store must be disabled")
	}
	rec := NewRecorder(g, "")
	if err := rec.Message(context.Background(), llm.Message{Role: llm.RoleUser, Content: "hey"}); err != nil {
		t.Fatal(err)
	}
	c := NewWebCache(g)
	if _, _, ok := c.Get(context.Background(), "https://x"); ok {
		t.Fatal("disabled cache")
	}
}
