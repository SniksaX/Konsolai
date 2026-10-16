package rag

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/siraj/konsolai/internal/redact"
	"github.com/siraj/konsolai/internal/store"
)

const (
	queueSize = 100
	FlushCap  = 3 * time.Second
)

type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type ChunkStore interface {
	InsertChunk(ctx context.Context, in store.ChunkIn) (bool, error)
	RAGAvailable(ctx context.Context) bool
}

type Item struct {
	Source    string
	Profile   string
	ProjectID *int64
	Content   string
	Ref       json.RawMessage
}

type Ingester struct {
	store   ChunkStore
	embed   Embedder
	profile string
	items   chan Item
	stop    chan struct{}
	mu      sync.Mutex
	looping bool
}

func New(st ChunkStore, emb Embedder, profile string) *Ingester {
	return &Ingester{
		store:   st,
		embed:   emb,
		profile: profile,
		items:   make(chan Item, queueSize),
		stop:    make(chan struct{}),
	}
}

func (i *Ingester) Start() {
	if i == nil {
		return
	}
	i.mu.Lock()
	if i.looping {
		i.mu.Unlock()
		return
	}
	i.looping = true
	i.mu.Unlock()
	go i.loop()
}

func (i *Ingester) Stop() {
	if i == nil {
		return
	}
	i.mu.Lock()
	if !i.looping {
		i.mu.Unlock()
		return
	}
	i.looping = false
	i.mu.Unlock()
	select {
	case <-i.stop:
	default:
		close(i.stop)
	}
}

func (i *Ingester) loop() {
	for {
		select {
		case <-i.stop:
			return
		case item := <-i.items:
			i.ingest(context.Background(), item)
		}
	}
}

func (i *Ingester) Enqueue(item Item) {
	if i == nil || strings.TrimSpace(item.Content) == "" {
		return
	}
	if skipSensitive(item.Ref) {
		return
	}
	if item.Profile == "" {
		item.Profile = i.profile
	}
	select {
	case i.items <- item:
	default:
	}
}

func (i *Ingester) EnqueueTurn(projectID *int64, prompt, answer, sessionID, title string, seq int) {
	if i == nil {
		return
	}
	body := strings.TrimSpace(prompt)
	if a := strings.TrimSpace(answer); a != "" {
		if body != "" {
			body += "\n\n"
		}
		body += a
	}
	if body == "" {
		return
	}
	ref, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"seq":        seq,
		"title":      title,
		"date":       time.Now().UTC().Format("2006-01-02"),
	})
	for _, chunk := range SplitTurn(body) {
		i.Enqueue(Item{Source: "turn", Profile: i.profile, ProjectID: projectID, Content: chunk, Ref: ref})
	}
}

func (i *Ingester) EnqueueSummary(projectID *int64, sessionID, summary string, compactedUpto int) {
	if i == nil {
		return
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return
	}
	ref, _ := json.Marshal(map[string]any{
		"session_id":     sessionID,
		"compacted_upto": compactedUpto,
	})
	i.Enqueue(Item{Source: "summary", Profile: i.profile, ProjectID: projectID, Content: summary, Ref: json.RawMessage(ref)})
}

func (i *Ingester) EnqueueWeb(projectID *int64, url, title, text string) {
	if i == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	ref, _ := json.Marshal(map[string]any{
		"url":        url,
		"title":      title,
		"fetched_at": time.Now().UTC().Format(time.RFC3339),
	})
	raw := json.RawMessage(ref)
	for _, chunk := range SplitWeb(text) {
		i.Enqueue(Item{Source: "web", Profile: i.profile, ProjectID: projectID, Content: chunk, Ref: raw})
	}
}

func (i *Ingester) Flush(ctx context.Context, d time.Duration) int {
	if i == nil {
		return 0
	}
	deadline := time.Now().Add(d)
	for {
		if time.Now().After(deadline) {
			return i.dropPending()
		}
		select {
		case item := <-i.items:
			i.ingest(ctx, item)
		default:
			return 0
		}
	}
}

func (i *Ingester) dropPending() int {
	n := 0
	for {
		select {
		case <-i.items:
			n++
		default:
			return n
		}
	}
}

func (i *Ingester) ingest(ctx context.Context, item Item) {
	if i.store == nil || i.embed == nil {
		return
	}
	if !i.store.RAGAvailable(ctx) {
		return
	}
	vecs, err := i.embed.Embed(ctx, []string{item.Content})
	if err != nil || len(vecs) == 0 {
		return
	}
	_, _ = i.store.InsertChunk(ctx, store.ChunkIn{
		Profile:   item.Profile,
		ProjectID: item.ProjectID,
		Source:    item.Source,
		Ref:       item.Ref,
		Content:   item.Content,
		Embedding: vecs[0],
	})
}

func skipSensitive(ref json.RawMessage) bool {
	if len(ref) == 0 {
		return false
	}
	var m map[string]any
	if json.Unmarshal(ref, &m) != nil {
		return false
	}
	path, _ := m["path"].(string)
	return path != "" && redact.SensitivePath(path)
}
