package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/SniksaX/Konsolai/internal/agent"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/store"
	"github.com/SniksaX/Konsolai/internal/tools"
)

type Backend interface {
	NextSeq(ctx context.Context, sessionID string) (int, error)
	AppendMessage(ctx context.Context, sessionID string, seq int, m store.Message) error
	TouchSession(ctx context.Context, id string) error
	RecordAction(ctx context.Context, a store.ActionIn) error
	RecordFileChange(ctx context.Context, c store.FileChangeIn) error
	GetWebCache(ctx context.Context, url string) (title, text string, ok bool, err error)
	PutWebCache(ctx context.Context, url, title, text string, ttl time.Duration) error
	AddUserFact(ctx context.Context, profile, fact, source string) (int64, error)
	AddProjectFact(ctx context.Context, projectID int64, fact string, sourceSession *string) (int64, error)
	Close()
}

var _ Backend = (*store.Store)(nil)

type Guard struct {
	mu       sync.Mutex
	store    Backend
	disabled bool
	onFail   func(error)
	isolated time.Duration
}

func isNilBackend(s Backend) bool {
	if s == nil {
		return true
	}
	st, ok := s.(*store.Store)
	return ok && st == nil
}

func NewGuard(s Backend, onFail func(error)) *Guard {
	if isNilBackend(s) {
		return &Guard{onFail: onFail, disabled: true}
	}
	return &Guard{store: s, onFail: onFail}
}

func (g *Guard) Disabled() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.disabled || g.store == nil
}

func (g *Guard) Store() *store.Store {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.disabled || g.store == nil {
		return nil
	}
	s, ok := g.store.(*store.Store)
	if !ok {
		return nil
	}
	return s
}

func (g *Guard) live() Backend {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.disabled || isNilBackend(g.store) {
		return nil
	}
	return g.store
}

// SetIsolatedTimeout sets the detached memory deadline. Zero keeps the built-in limit.
func (g *Guard) SetIsolatedTimeout(d time.Duration) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.isolated = d
	g.mu.Unlock()
}

// Isolated detaches ctx and bounds the call by the guard's timeout.
func (g *Guard) Isolated(ctx context.Context) (context.Context, context.CancelFunc) {
	d := time.Duration(0)
	if g != nil {
		g.mu.Lock()
		d = g.isolated
		g.mu.Unlock()
	}
	if d <= 0 {
		d = config.Builtin().IsolatedTimeout()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), d)
}

func Isolated(ctx context.Context) (context.Context, context.CancelFunc) {
	return (&Guard{}).Isolated(ctx)
}

func CausedByCancel(err error) bool {
	return err != nil && errors.Is(err, context.Canceled)
}

func (g *Guard) Fail(err error) {
	if CausedByCancel(err) {
		return
	}
	g.disable(err)
}

func (g *Guard) disable(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.disabled {
		return
	}
	g.disabled = true
	if g.store != nil {
		g.store.Close()
		g.store = nil
	}
	if g.onFail != nil {
		g.onFail(err)
	}
}

func (g *Guard) Do(fn func(Backend) error) {
	s := g.live()
	if s == nil {
		return
	}
	if err := fn(s); err != nil {
		if CausedByCancel(err) {
			return
		}
		g.disable(err)
	}
}

type Recorder struct {
	g         *Guard
	sessionID string
	cwd       string
}

func NewRecorder(g *Guard, sessionID, cwd string) *Recorder {
	return &Recorder{g: g, sessionID: sessionID, cwd: cwd}
}

func (r *Recorder) Message(ctx context.Context, m llm.Message) error {
	r.g.Do(func(s Backend) error {
		seq, err := s.NextSeq(ctx, r.sessionID)
		if err != nil {
			return err
		}
		var calls []byte
		if len(m.ToolCalls) > 0 {
			calls, err = json.Marshal(m.ToolCalls)
			if err != nil {
				return err
			}
		}
		if err := s.AppendMessage(ctx, r.sessionID, seq, store.Message{
			Role: string(m.Role), Content: m.Content, ToolCalls: calls, ToolCallID: m.ToolCallID, CWD: r.cwd,
		}); err != nil {
			return err
		}
		return s.TouchSession(ctx, r.sessionID)
	})
	return nil
}

func (r *Recorder) Action(ctx context.Context, a agent.ActionRecord) error {
	r.g.Do(func(s Backend) error {
		seq, err := s.NextSeq(ctx, r.sessionID)
		if err != nil {
			return err
		}
		if err := s.RecordAction(ctx, store.ActionIn{
			SessionID: r.sessionID, Seq: seq, Tool: a.Tool, Args: a.Args,
			Kind: a.Kind.String(), Decision: a.Decision, RejectReason: a.RejectReason,
			IsError: a.IsError, Meta: a.Meta, Duration: a.Duration, Output: a.Output,
		}); err != nil {
			return err
		}
		return s.TouchSession(ctx, r.sessionID)
	})
	return nil
}

func (r *Recorder) FileChange(ctx context.Context, c tools.FileChange) error {
	r.g.Do(func(s Backend) error {
		seq, err := s.NextSeq(ctx, r.sessionID)
		if err != nil {
			return err
		}
		if err := s.RecordFileChange(ctx, store.FileChangeIn{
			SessionID: r.sessionID, Seq: seq, Path: c.Path, ExistedBefore: c.ExistedBefore,
			Before: c.Before, AfterHash: c.AfterHash, Diff: c.Diff,
		}); err != nil {
			return err
		}
		return s.TouchSession(ctx, r.sessionID)
	})
	return nil
}

var (
	_ agent.Recorder  = (*Recorder)(nil)
	_ tools.WebCache  = (*WebCache)(nil)
	_ tools.FactStore = (*FactStore)(nil)
)

type WebCache struct{ g *Guard }

func NewWebCache(g *Guard) *WebCache { return &WebCache{g: g} }

func (w *WebCache) Get(ctx context.Context, url string) (title, text string, ok bool) {
	s := w.g.live()
	if s == nil {
		return "", "", false
	}
	title, text, ok, err := s.GetWebCache(ctx, url)
	if err != nil {
		w.g.disable(err)
		return "", "", false
	}
	return title, text, ok
}

func (w *WebCache) Put(ctx context.Context, url, title, text string, ttl time.Duration) error {
	w.g.Do(func(s Backend) error {
		return s.PutWebCache(ctx, url, title, text, ttl)
	})
	return nil
}

type FactStore struct {
	g         *Guard
	profile   string
	projectID *int64
	sessionID string
}

func NewFactStore(g *Guard, profile string, projectID *int64, sessionID string) *FactStore {
	return &FactStore{g: g, profile: profile, projectID: projectID, sessionID: sessionID}
}

func (f *FactStore) Add(ctx context.Context, fact, scope string) error {
	s := f.g.live()
	if s == nil {
		return nil
	}
	var err error
	switch scope {
	case "project":
		if f.projectID == nil {
			return fmt.Errorf("memory: no project for this directory")
		}
		var sid *string
		if f.sessionID != "" {
			sid = &f.sessionID
		}
		_, err = s.AddProjectFact(ctx, *f.projectID, fact, sid)
	default:
		_, err = s.AddUserFact(ctx, f.profile, fact, "explicit")
	}
	if err != nil {
		f.g.disable(err)
		return err
	}
	return nil
}

func ToLLM(msgs []store.Message) []llm.Message {
	out := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		lm := llm.Message{Role: llm.Role(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		if len(m.ToolCalls) > 0 {
			_ = json.Unmarshal(m.ToolCalls, &lm.ToolCalls)
		}
		out = append(out, lm)
	}
	return out
}
