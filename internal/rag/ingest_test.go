package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/siraj/konsolai/internal/store"
)

func TestSplitTurnSizes(t *testing.T) {
	short := "hello world"
	got := SplitTurn(short)
	if len(got) != 1 || got[0] != short {
		t.Fatalf("%q", got)
	}
	if SplitTurn("  ") != nil {
		t.Fatal("empty")
	}
	long := strings.Repeat("x", 9000)
	parts := SplitTurn(long)
	if len(parts) < 2 {
		t.Fatalf("want split, got %d", len(parts))
	}
	for i, p := range parts {
		if estimateTokens(p) > turnMaxTokens+5 {
			t.Fatalf("chunk %d tokens %d", i, estimateTokens(p))
		}
	}
	headed := "# One\n" + strings.Repeat("a", 2500) + "\n# Two\n" + strings.Repeat("b", 2500)
	hs := SplitTurn(headed)
	if len(hs) < 2 {
		t.Fatalf("headings %d tokens=%d", len(hs), estimateTokens(headed))
	}
	joined := strings.Join(hs, "\n")
	if !strings.Contains(joined, "# One") || !strings.Contains(joined, "# Two") {
		t.Fatalf("%q", joined)
	}
	paras := strings.Repeat("para one word. ", 200) + "\n\n" + strings.Repeat("para two word. ", 200) + "\n\n" + strings.Repeat("para three word. ", 200)
	ps := SplitTurn(paras)
	if len(ps) < 2 {
		t.Fatalf("paragraphs %d", len(ps))
	}
}

func TestSplitWebOverlap(t *testing.T) {
	s := strings.Repeat("abcdefghijklmnopqrstuvwxyz ", 400)
	parts := SplitWeb(s)
	if len(parts) < 2 {
		t.Fatalf("want windows, got %d", len(parts))
	}
	for i, p := range parts {
		if estimateTokens(p) > webMaxTokens+10 {
			t.Fatalf("chunk %d tokens %d", i, estimateTokens(p))
		}
	}
	size, overlap := runeWindow(webMaxTokens, webOverlapTok)
	if overlap < 1 || utf8.RuneCountInString(parts[0]) > size+2 {
		t.Fatalf("window size=%d overlap=%d first=%d", size, overlap, utf8.RuneCountInString(parts[0]))
	}
	a := []rune(parts[0])
	b := []rune(parts[1])
	if len(a) < overlap/2 || len(b) < overlap/2 {
		t.Fatal("too short to overlap")
	}
	shared := false
	limit := overlap
	if limit > len(a) {
		limit = len(a)
	}
	if limit > len(b) {
		limit = len(b)
	}
	for n := overlap / 2; n <= limit; n++ {
		if string(a[len(a)-n:]) == string(b[:n]) {
			shared = true
			break
		}
	}
	if !shared {
		t.Fatalf("no overlap between consecutive web chunks")
	}
}

type fakeEmbed struct {
	delay time.Duration
	calls int
	mu    sync.Mutex
}

func (f *fakeEmbed) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	out := make([][]float32, len(texts))
	for i, s := range texts {
		v := make([]float32, 8)
		v[0] = float32(len(s))
		out[i] = v
	}
	return out, nil
}

type memStore struct {
	mu     sync.Mutex
	rows   []store.ChunkIn
	hashes map[string]struct{}
	avail  bool
}

func (m *memStore) RAGAvailable(context.Context) bool { return m.avail }

func (m *memStore) InsertChunk(_ context.Context, in store.ChunkIn) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hashes == nil {
		m.hashes = map[string]struct{}{}
	}
	h := store.ContentHash(in.Content)
	if _, ok := m.hashes[h]; ok {
		return false, nil
	}
	m.hashes[h] = struct{}{}
	m.rows = append(m.rows, in)
	return true, nil
}

func (m *memStore) inserted() []store.ChunkIn {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]store.ChunkIn(nil), m.rows...)
}

func TestIngestDedupeByHash(t *testing.T) {
	st := &memStore{avail: true}
	ing := New(st, &fakeEmbed{}, "home")
	body := "same turn content for hash"
	ing.EnqueueTurn(nil, body, "", "sid", "t", 1)
	ing.EnqueueTurn(nil, body, "", "sid", "t", 2)
	if n := ing.Flush(context.Background(), time.Second); n != 0 {
		t.Fatalf("dropped %d", n)
	}
	rows := st.inserted()
	if len(rows) != 1 {
		t.Fatalf("want 1 unique, got %d", len(rows))
	}
	if rows[0].Source != "turn" {
		t.Fatalf("source %s", rows[0].Source)
	}
}

func TestIngestSensitiveExclusion(t *testing.T) {
	st := &memStore{avail: true}
	ing := New(st, &fakeEmbed{}, "home")
	ref, _ := json.Marshal(map[string]string{"path": ".env"})
	ing.Enqueue(Item{Source: "code", Content: "SECRET=1", Ref: ref})
	ing.Enqueue(Item{Source: "turn", Content: "ok to keep", Ref: json.RawMessage(`{}`)})
	_ = ing.Flush(context.Background(), time.Second)
	rows := st.inserted()
	if len(rows) != 1 || rows[0].Content != "ok to keep" {
		t.Fatalf("%+v", rows)
	}
}

func TestFlushCapDropsPending(t *testing.T) {
	st := &memStore{avail: true}
	ing := New(st, &fakeEmbed{delay: 80 * time.Millisecond}, "home")
	for i := 0; i < 20; i++ {
		ing.Enqueue(Item{Source: "turn", Content: strings.Repeat("x", 10) + string(rune('a'+i))})
	}
	dropped := ing.Flush(context.Background(), 50*time.Millisecond)
	if dropped < 1 {
		t.Fatalf("want drops, got %d inserted=%d", dropped, len(st.inserted()))
	}
}

type refuseEmbed struct{}

func (refuseEmbed) Embed(context.Context, []string) ([][]float32, error) {
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
}

func TestIngestPausesWhenUnreachable(t *testing.T) {
	st := &memStore{avail: true}
	var notice bytes.Buffer
	ing := New(st, refuseEmbed{}, "home")
	ing.SetNotice(&notice)
	ing.EnqueueTurn(nil, "hello world", "ans", "sid", "t", 1)
	_ = ing.Flush(context.Background(), time.Second)
	if !ing.Paused() {
		t.Fatal("want paused")
	}
	if !strings.Contains(notice.String(), "memory ingest paused (embed endpoint unreachable)") {
		t.Fatalf("notice %q", notice.String())
	}
	ing.EnqueueTurn(nil, "another", "", "sid", "t", 2)
	_ = ing.Flush(context.Background(), time.Second)
	if len(st.inserted()) != 0 {
		t.Fatalf("turns waited or ingested: %+v", st.inserted())
	}
}
