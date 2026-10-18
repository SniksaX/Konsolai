package store

import (
	"context"
	"fmt"
)

// TurnStat is one model call. An empty SessionID is stored as NULL.
type TurnStat struct {
	SessionID        string
	Intent           string
	Model            string
	Endpoint         string
	PromptTokens     int
	CompletionTokens int
	ModelMS          int
}

// RecordTurnStat inserts one turn_stats row. Callers treat a returned error
// as best-effort: it must not change the turn that produced the call.
func (s *Store) RecordTurnStat(ctx context.Context, st TurnStat) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("store: record turn stat: no pool")
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	var sid *string
	if st.SessionID != "" {
		id := st.SessionID
		sid = &id
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO turn_stats (
			session_id, intent, model, endpoint,
			prompt_tokens, completion_tokens, model_ms
		) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)`,
		sid, st.Intent, st.Model, st.Endpoint,
		st.PromptTokens, st.CompletionTokens, st.ModelMS)
	if err != nil {
		return fmt.Errorf("store: record turn stat: %w", err)
	}
	return nil
}

// ModelRate is completion tokens per second over the last N calls for model.
// lastN <= 0 means 20. The result is nil when there is no positive model_ms
// to divide by (no rows, or every retained row has model_ms = 0).
func (s *Store) ModelRate(ctx context.Context, model string, lastN int) (*float64, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("store: model rate: no pool")
	}
	if lastN <= 0 {
		lastN = 20
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	var tokens, ms int64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(completion_tokens), 0), COALESCE(SUM(model_ms), 0)
		FROM (
			SELECT completion_tokens, model_ms
			FROM turn_stats
			WHERE model = $1
			ORDER BY created_at DESC
			LIMIT $2
		) recent`, model, lastN).Scan(&tokens, &ms)
	if err != nil {
		return nil, fmt.Errorf("store: model rate: %w", err)
	}
	if ms <= 0 {
		return nil, nil
	}
	rate := float64(tokens) / (float64(ms) / 1000)
	return &rate, nil
}
