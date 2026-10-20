package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/SniksaX/Konsolai/internal/config"
)

// CurrentSchema is the schema unqualified tables use on this store.
func (s *Store) CurrentSchema(ctx context.Context) (string, error) {
	if s == nil || s.pool == nil {
		return "", fmt.Errorf("store: current schema: no pool")
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	var name string
	if err := s.pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&name); err != nil {
		return "", fmt.Errorf("store: current schema: %w", err)
	}
	return name, nil
}

// EmbeddingDim reads the memory_chunks.embedding vector width from the catalog.
// pgvector stores that width as the type modifier; format_type renders vector(N).
func (s *Store) EmbeddingDim(ctx context.Context) (int, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("store: embedding dim: no pool")
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	var typmod int
	var formatted string
	err := s.pool.QueryRow(ctx, `
		SELECT a.atttypmod, format_type(a.atttypid, a.atttypmod)
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_type t ON t.oid = a.atttypid
		WHERE c.relname = 'memory_chunks'
		  AND n.nspname = current_schema()
		  AND a.attname = 'embedding'
		  AND a.attnum > 0
		  AND NOT a.attisdropped
		  AND t.typname = 'vector'`).Scan(&typmod, &formatted)
	if err != nil {
		return 0, fmt.Errorf("store: embedding dim: %w", err)
	}
	n, err := vectorWidth(formatted, typmod)
	if err != nil {
		return 0, fmt.Errorf("store: embedding dim: %w", err)
	}
	return n, nil
}

func vectorWidth(formatted string, typmod int) (int, error) {
	const marker = "vector("
	if i := strings.LastIndex(formatted, marker); i >= 0 {
		rest := formatted[i+len(marker):]
		if j := strings.IndexByte(rest, ')'); j > 0 {
			n, err := strconv.Atoi(rest[:j])
			if err == nil && n > 0 {
				return n, nil
			}
		}
	}
	if typmod > 0 {
		return typmod, nil
	}
	return 0, fmt.Errorf("unparsed %q typmod %d", formatted, typmod)
}

// ResetEmbeddingDim drops and recreates memory_chunks and memory_files at dim.
// Sessions, facts, and turn_stats stay. memory_files is dropped first because
// a foreign key from that table to memory_chunks would otherwise block the drop.
func (s *Store) ResetEmbeddingDim(ctx context.Context, dim int) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("store: reset embedding dim: no pool")
	}
	if dim < 1 || dim > 16000 {
		return fmt.Errorf("store: reset embedding dim: %d", dim)
	}
	d := s.migrateTimeout
	if d <= 0 {
		d = config.Builtin().StoreMigrateTimeout()
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: reset embedding dim: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('konsolai_migrations'))"); err != nil {
		return fmt.Errorf("store: reset embedding dim: %w", err)
	}
	if _, err := tx.Exec(ctx, `DROP TABLE IF EXISTS memory_files`); err != nil {
		return fmt.Errorf("store: reset embedding dim: drop memory_files: %w", err)
	}
	if _, err := tx.Exec(ctx, `DROP TABLE IF EXISTS memory_chunks`); err != nil {
		return fmt.Errorf("store: reset embedding dim: drop memory_chunks: %w", err)
	}
	body, err := migrationFS.ReadFile("migrations/0007_rag.sql")
	if err != nil {
		return fmt.Errorf("store: reset embedding dim: %w", err)
	}
	sql := strings.ReplaceAll(string(body), "__DIM__", strconv.Itoa(dim))
	for _, stmt := range splitSQL(sql) {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("store: reset embedding dim: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO meta (key, value) VALUES ('rag', 'ready')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`); err != nil {
		return fmt.Errorf("store: reset embedding dim: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: reset embedding dim: %w", err)
	}
	return nil
}
