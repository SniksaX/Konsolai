package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type Chunk struct {
	ID        int64
	Profile   string
	ProjectID *int64
	Source    string
	Ref       json.RawMessage
	Content   string
	Hash      string
	CreatedAt time.Time
}

type ChunkIn struct {
	Profile   string
	ProjectID *int64
	Source    string
	Ref       json.RawMessage
	Content   string
	Embedding []float32
}

type MemoryFile struct {
	Path      string
	SHA256    string
	IndexedAt time.Time
}

func ContentHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func VectorLiteral(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(x), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

func (s *Store) Meta(ctx context.Context, key string) (string, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	var v string
	err := s.pool.QueryRow(ctx, `SELECT value FROM meta WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: meta: %w", err)
	}
	return v, nil
}

func (s *Store) RAGAvailable(ctx context.Context) bool {
	v, err := s.Meta(ctx, "rag")
	return err == nil && v == "ready"
}

func (s *Store) InsertChunk(ctx context.Context, in ChunkIn) (bool, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if in.Ref == nil {
		in.Ref = json.RawMessage(`{}`)
	}
	hash := ContentHash(in.Content)
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO memory_chunks (profile, project_id, source, ref, content, content_hash, embedding)
		VALUES ($1, $2, $3, $4, $5, $6, $7::vector)
		ON CONFLICT (profile, source, content_hash) DO NOTHING
		RETURNING id`,
		in.Profile, in.ProjectID, in.Source, in.Ref, in.Content, hash, VectorLiteral(in.Embedding),
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: insert chunk: %w", err)
	}
	return true, nil
}

func (s *Store) DeleteCodeChunks(ctx context.Context, projectID int64, path string) error {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	_, err := s.pool.Exec(ctx, `
		DELETE FROM memory_chunks
		WHERE source = 'code' AND project_id = $1 AND ref->>'path' = $2`, projectID, path)
	if err != nil {
		return fmt.Errorf("store: delete code chunks: %w", err)
	}
	return nil
}

func (s *Store) UpsertMemoryFile(ctx context.Context, projectID int64, path, sha string) error {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO memory_files (project_id, path, sha256, indexed_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (project_id, path) DO UPDATE SET sha256 = EXCLUDED.sha256, indexed_at = now()`,
		projectID, path, sha)
	if err != nil {
		return fmt.Errorf("store: upsert memory file: %w", err)
	}
	return nil
}

func (s *Store) MemoryFileSHA(ctx context.Context, projectID int64, path string) (string, bool, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	var sha string
	err := s.pool.QueryRow(ctx, `
		SELECT sha256 FROM memory_files WHERE project_id = $1 AND path = $2`, projectID, path).Scan(&sha)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: memory file: %w", err)
	}
	return sha, true, nil
}

func (s *Store) ListMemoryFiles(ctx context.Context, projectID int64) ([]MemoryFile, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := s.pool.Query(ctx, `
		SELECT path, sha256, indexed_at FROM memory_files WHERE project_id = $1`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: list memory files: %w", err)
	}
	defer rows.Close()
	var out []MemoryFile
	for rows.Next() {
		var f MemoryFile
		if err := rows.Scan(&f.Path, &f.SHA256, &f.IndexedAt); err != nil {
			return nil, fmt.Errorf("store: scan memory file: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) DeleteMemoryFile(ctx context.Context, projectID int64, path string) error {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if _, err := s.pool.Exec(ctx, `DELETE FROM memory_files WHERE project_id = $1 AND path = $2`, projectID, path); err != nil {
		return fmt.Errorf("store: delete memory file: %w", err)
	}
	return nil
}

func (s *Store) ChunkCounts(ctx context.Context, profile string, projectID *int64) (map[string]int, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := s.pool.Query(ctx, `
		SELECT source, count(*) FROM memory_chunks
		WHERE profile = $1 AND ($2::bigint IS NULL OR project_id = $2 OR source <> 'code')
		GROUP BY source`, profile, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: chunk counts: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var src string
		var n int
		if err := rows.Scan(&src, &n); err != nil {
			return nil, fmt.Errorf("store: scan chunk count: %w", err)
		}
		out[src] = n
	}
	return out, rows.Err()
}

func (s *Store) LastIndexedAt(ctx context.Context, projectID int64) (time.Time, bool, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	var t time.Time
	err := s.pool.QueryRow(ctx, `SELECT max(indexed_at) FROM memory_files WHERE project_id = $1`, projectID).Scan(&t)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("store: last indexed: %w", err)
	}
	if t.IsZero() {
		return time.Time{}, false, nil
	}
	return t, true, nil
}
