package store

import (
	"context"
	"fmt"
	"time"
)

// ModelStat is one model's calls inside a stats window.
type ModelStat struct {
	Model            string
	Calls            int
	PromptTokens     int
	CompletionTokens int
	ReasoningTokens  int
	// TokPerSec is nil when every call in the window has model_ms = 0.
	TokPerSec *float64
	P50MS     float64
	P95MS     float64
}

// IntentStat aggregates turn_stats rows for one intent.
// AvgSteps is the mean number of turn_stats rows per session for that intent
// (a null session_id is its own bucket). AvgWallMS is the mean of model_ms;
// the schema has no separate wall-clock column.
type IntentStat struct {
	Intent    string
	Turns     int
	AvgSteps  float64
	AvgWallMS float64
}

// UsageTotals counts sessions by started_at and turns/tokens by turn_stats.
type UsageTotals struct {
	Sessions int
	Turns    int
	Tokens   int
}

// UsageReport is the /stats window.
type UsageReport struct {
	Models  []ModelStat
	Intents []IntentStat
	Totals  UsageTotals
}

// UsageReport loads per-model, per-intent, and total figures.
// since nil means the whole table. Sessions are those with started_at in
// the window; turns and tokens come from turn_stats.created_at.
func (s *Store) UsageReport(ctx context.Context, since *time.Time) (UsageReport, error) {
	if s == nil || s.pool == nil {
		return UsageReport{}, fmt.Errorf("store: usage report: no pool")
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	var sinceArg any
	if since != nil {
		sinceArg = *since
	}
	rep, err := s.modelStats(ctx, sinceArg)
	if err != nil {
		return UsageReport{}, err
	}
	rep.Intents, err = s.intentStats(ctx, sinceArg)
	if err != nil {
		return UsageReport{}, err
	}
	rep.Totals, err = s.usageTotals(ctx, sinceArg)
	if err != nil {
		return UsageReport{}, err
	}
	return rep, nil
}

func (s *Store) modelStats(ctx context.Context, since any) (UsageReport, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT model,
			count(*)::int,
			COALESCE(SUM(prompt_tokens), 0)::int,
			COALESCE(SUM(completion_tokens), 0)::int,
			COALESCE(SUM(reasoning_tokens), 0)::int,
			COALESCE(SUM(completion_tokens) FILTER (WHERE model_ms > 0), 0)::float8,
			COALESCE(SUM(model_ms) FILTER (WHERE model_ms > 0), 0)::float8,
			COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY model_ms), 0)::float8,
			COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY model_ms), 0)::float8
		FROM turn_stats
		WHERE ($1::timestamptz IS NULL OR created_at >= $1)
		GROUP BY model
		ORDER BY model`, since)
	if err != nil {
		return UsageReport{}, fmt.Errorf("store: usage models: %w", err)
	}
	defer rows.Close()
	var rep UsageReport
	for rows.Next() {
		var row ModelStat
		var compPos, msPos float64
		if err := rows.Scan(&row.Model, &row.Calls, &row.PromptTokens, &row.CompletionTokens, &row.ReasoningTokens,
			&compPos, &msPos, &row.P50MS, &row.P95MS); err != nil {
			return UsageReport{}, fmt.Errorf("store: usage models: %w", err)
		}
		if msPos > 0 {
			rate := compPos / (msPos / 1000)
			row.TokPerSec = &rate
		}
		rep.Models = append(rep.Models, row)
	}
	if err := rows.Err(); err != nil {
		return UsageReport{}, fmt.Errorf("store: usage models: %w", err)
	}
	return rep, nil
}

func (s *Store) intentStats(ctx context.Context, since any) ([]IntentStat, error) {
	rows, err := s.pool.Query(ctx, `
		WITH rows AS (
			SELECT intent,
				COALESCE(session_id::text, 'id:' || id::text) AS bucket,
				model_ms
			FROM turn_stats
			WHERE ($1::timestamptz IS NULL OR created_at >= $1)
		),
		per AS (
			SELECT intent, bucket, count(*) AS steps, avg(model_ms)::float8 AS wall
			FROM rows
			GROUP BY intent, bucket
		)
		SELECT intent,
			COALESCE(SUM(steps), 0)::int,
			COALESCE(AVG(steps), 0)::float8,
			COALESCE(AVG(wall), 0)::float8
		FROM per
		GROUP BY intent
		ORDER BY intent`, since)
	if err != nil {
		return nil, fmt.Errorf("store: usage intents: %w", err)
	}
	defer rows.Close()
	var out []IntentStat
	for rows.Next() {
		var row IntentStat
		if err := rows.Scan(&row.Intent, &row.Turns, &row.AvgSteps, &row.AvgWallMS); err != nil {
			return nil, fmt.Errorf("store: usage intents: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: usage intents: %w", err)
	}
	return out, nil
}

func (s *Store) usageTotals(ctx context.Context, since any) (UsageTotals, error) {
	var tot UsageTotals
	err := s.pool.QueryRow(ctx, `
		SELECT count(*)::int FROM sessions
		WHERE ($1::timestamptz IS NULL OR started_at >= $1)`, since).Scan(&tot.Sessions)
	if err != nil {
		return UsageTotals{}, fmt.Errorf("store: usage sessions: %w", err)
	}
	err = s.pool.QueryRow(ctx, `
		SELECT count(*)::int,
			COALESCE(SUM(prompt_tokens + completion_tokens), 0)::int
		FROM turn_stats
		WHERE ($1::timestamptz IS NULL OR created_at >= $1)`, since).Scan(&tot.Turns, &tot.Tokens)
	if err != nil {
		return UsageTotals{}, fmt.Errorf("store: usage totals: %w", err)
	}
	return tot, nil
}
