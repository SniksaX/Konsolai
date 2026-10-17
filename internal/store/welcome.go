package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// WelcomeRow is one session line for the startup screen.
type WelcomeRow struct {
	Title     string
	FirstUser string
	Intent    string
	CWD       string
	LastAt    time.Time
}

const welcomeSessionCols = `
	s.title,
	COALESCE((
		SELECT m.content FROM messages m
		WHERE m.session_id = s.id AND m.role = 'user'
		ORDER BY m.seq ASC
		LIMIT 1
	), ''),
	COALESCE((
		SELECT t.intent FROM turn_stats t
		WHERE t.session_id = s.id
		ORDER BY t.created_at DESC, t.id DESC
		LIMIT 1
	), s.mode),
	s.cwd,
	s.last_activity_at`

// WelcomeMemory counts facts and sessions for the profile in one query.
func (s *Store) WelcomeMemory(ctx context.Context, profile string, projectID *int64) (facts, sessions int, err error) {
	if s == nil || s.pool == nil {
		return 0, 0, fmt.Errorf("store: welcome memory: no pool")
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	err = s.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM user_facts WHERE profile = $1)
			+ CASE
				WHEN $2::bigint IS NULL THEN 0
				ELSE (SELECT count(*) FROM project_facts WHERE project_id = $2)
			  END,
			(SELECT count(*) FROM sessions WHERE profile = $1)`,
		profile, projectID).Scan(&facts, &sessions)
	if err != nil {
		return 0, 0, fmt.Errorf("store: welcome memory: %w", err)
	}
	return facts, sessions, nil
}

// WelcomeRecent returns up to limit sessions, current directory first, and the
// single most recently active session (for the resume placeholder).
func (s *Store) WelcomeRecent(ctx context.Context, profile, cwd string, limit int) (display []WelcomeRow, latest WelcomeRow, hasLatest bool, err error) {
	if s == nil || s.pool == nil {
		return nil, WelcomeRow{}, false, fmt.Errorf("store: welcome recent: no pool")
	}
	if limit < 1 {
		limit = 4
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()

	err = s.pool.QueryRow(ctx, `
		SELECT `+welcomeSessionCols+`
		FROM sessions s
		WHERE s.profile = $1
		ORDER BY s.last_activity_at DESC
		LIMIT 1`, profile).Scan(&latest.Title, &latest.FirstUser, &latest.Intent, &latest.CWD, &latest.LastAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, WelcomeRow{}, false, nil
	}
	if err != nil {
		return nil, WelcomeRow{}, false, fmt.Errorf("store: welcome latest: %w", err)
	}
	hasLatest = true

	rows, err := s.pool.Query(ctx, `
		SELECT `+welcomeSessionCols+`
		FROM sessions s
		WHERE s.profile = $1
		ORDER BY (s.cwd = $2) DESC, s.last_activity_at DESC
		LIMIT $3`, profile, cwd, limit)
	if err != nil {
		return nil, latest, true, fmt.Errorf("store: welcome recent: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row WelcomeRow
		if err := rows.Scan(&row.Title, &row.FirstUser, &row.Intent, &row.CWD, &row.LastAt); err != nil {
			return nil, latest, true, fmt.Errorf("store: scan welcome recent: %w", err)
		}
		display = append(display, row)
	}
	if err := rows.Err(); err != nil {
		return nil, latest, true, fmt.Errorf("store: welcome recent: %w", err)
	}
	return display, latest, true, nil
}
