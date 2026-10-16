package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

type SearchHit struct {
	ID      int64
	Source  string
	Ref     json.RawMessage
	Content string
}

func sourceClause(scope string) string {
	switch scope {
	case "sessions":
		return " AND source IN ('turn','summary')"
	case "code":
		return " AND source = 'code'"
	case "web":
		return " AND source = 'web'"
	default:
		return ""
	}
}

const searchFilter = `
		profile = $1
		AND (source <> 'code' OR ($2::bigint IS NOT NULL AND project_id = $2))`

func (s *Store) SearchVector(ctx context.Context, profile string, projectID *int64, scope string, vec []float32, limit int) ([]SearchHit, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if limit < 1 {
		limit = 30
	}
	q := `
		SELECT id, source, ref, content
		FROM memory_chunks
		WHERE ` + searchFilter + sourceClause(scope) + `
		ORDER BY embedding <=> $3::vector
		LIMIT $4`
	rows, err := s.pool.Query(ctx, q, profile, projectID, VectorLiteral(vec), limit)
	if err != nil {
		return nil, fmt.Errorf("store: search vector: %w", err)
	}
	defer rows.Close()
	return scanHits(rows)
}

func (s *Store) SearchText(ctx context.Context, profile string, projectID *int64, scope, query string, limit int) ([]SearchHit, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if limit < 1 {
		limit = 30
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	q := `
		SELECT id, source, ref, content
		FROM memory_chunks
		WHERE ` + searchFilter + sourceClause(scope) + `
		  AND tsv @@ websearch_to_tsquery('simple', unaccent($3))
		ORDER BY ts_rank(tsv, websearch_to_tsquery('simple', unaccent($3))) DESC
		LIMIT $4`
	rows, err := s.pool.Query(ctx, q, profile, projectID, query, limit)
	if err != nil {
		return nil, fmt.Errorf("store: search text: %w", err)
	}
	defer rows.Close()
	return scanHits(rows)
}

func scanHits(rows pgx.Rows) ([]SearchHit, error) {
	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.ID, &h.Source, &h.Ref, &h.Content); err != nil {
			return nil, fmt.Errorf("store: scan search hit: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
