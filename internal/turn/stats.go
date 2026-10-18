package turn

import (
	"context"
	"time"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/store"
)

// beginStats arms a best-effort recorder for model calls during this turn.
// A store failure never changes the turn result.
func (r *Runner) beginStats(intent string) {
	if r == nil {
		return
	}
	if intent == "" {
		intent = "answer"
	}
	r.statMu.Lock()
	r.statOn = true
	r.statIntent = intent
	r.statSession = ""
	r.statMu.Unlock()
	r.attachStats(r.Client)
	r.attachStats(r.FastClient)
}

func (r *Runner) endStats() {
	if r == nil {
		return
	}
	r.statMu.Lock()
	r.statOn = false
	r.statMu.Unlock()
	if r.Client != nil {
		r.Client.SetCallObserver(nil)
	}
	if r.FastClient != nil {
		r.FastClient.SetCallObserver(nil)
	}
}

func (r *Runner) setStatIntent(intent string) {
	if r == nil || intent == "" {
		return
	}
	r.statMu.Lock()
	r.statIntent = intent
	r.statMu.Unlock()
}

func (r *Runner) setStatSession(id string) {
	if r == nil {
		return
	}
	r.statMu.Lock()
	r.statSession = id
	r.statMu.Unlock()
}

func (r *Runner) attachStats(c *llm.Client) {
	if r == nil || c == nil {
		return
	}
	r.statMu.Lock()
	on := r.statOn
	r.statMu.Unlock()
	if !on {
		return
	}
	c.SetCallObserver(func(info llm.CallInfo) {
		r.recordCall(info)
	})
}

func (r *Runner) statsInsertTimeout() time.Duration {
	if r == nil {
		return config.Builtin().StatsInsertTimeout()
	}
	return r.Profile.StatsInsertTimeout()
}

func (r *Runner) recordCall(info llm.CallInfo) {
	defer func() { _ = recover() }()
	if r == nil {
		return
	}
	st := r.mem().Store()
	if st == nil {
		return
	}
	r.statMu.Lock()
	intent := r.statIntent
	sid := r.statSession
	r.statMu.Unlock()
	if intent == "" {
		intent = "answer"
	}
	model := info.Model
	if model == "" {
		model = "-"
	}
	endpoint := info.Endpoint
	if endpoint == "" {
		endpoint = "-"
	}
	row := store.TurnStat{
		SessionID:        sid,
		Intent:           intent,
		Model:            model,
		Endpoint:         endpoint,
		PromptTokens:     info.PromptTokens,
		CompletionTokens: info.CompletionTokens,
		ModelMS:          info.ModelMS,
	}
	go func() {
		defer func() { _ = recover() }()
		ctx, cancel := context.WithTimeout(context.Background(), r.statsInsertTimeout())
		defer cancel()
		_ = st.RecordTurnStat(ctx, row)
	}()
}
