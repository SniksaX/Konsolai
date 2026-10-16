package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

const (
	minServerVersion = 130000
	migrateTimeout   = 15 * time.Second
	EmbeddingDim     = 1024
)

func (s *Store) migrate(ctx context.Context) error {
	qctx, cancel := context.WithTimeout(ctx, migrateTimeout)
	defer cancel()

	var ver int
	if err := s.pool.QueryRow(qctx, "SELECT current_setting('server_version_num')::int").Scan(&ver); err != nil {
		return fmt.Errorf("store: server version: %w", err)
	}
	if ver < minServerVersion {
		return fmt.Errorf("store: PostgreSQL 13+ required (server_version_num=%d)", ver)
	}

	tx, err := s.pool.Begin(qctx)
	if err != nil {
		return fmt.Errorf("store: begin migrate: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if _, err := tx.Exec(qctx, "SELECT pg_advisory_xact_lock(hashtext('konsolai_migrations'))"); err != nil {
		return fmt.Errorf("store: advisory lock: %w", err)
	}
	if _, err := tx.Exec(qctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version int PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("store: schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("store: read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			return err
		}
		var applied int
		err = tx.QueryRow(qctx, "SELECT 1 FROM schema_migrations WHERE version = $1", version).Scan(&applied)
		if err == nil {
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("store: check migration %d: %w", version, err)
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("store: read %s: %w", name, err)
		}
		if version == 7 {
			if err := applyRAGMigration(qctx, tx, string(body), "vector"); err != nil {
				return err
			}
		} else {
			for _, stmt := range splitSQL(string(body)) {
				if _, err := tx.Exec(qctx, stmt); err != nil {
					return fmt.Errorf("store: apply %s: %w", name, err)
				}
			}
		}
		if _, err := tx.Exec(qctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version); err != nil {
			return fmt.Errorf("store: record %s: %w", name, err)
		}
	}
	if err := tx.Commit(qctx); err != nil {
		return fmt.Errorf("store: commit migrate: %w", err)
	}
	return nil
}

func migrationVersion(name string) (int, error) {
	i := 0
	for i < len(name) && unicode.IsDigit(rune(name[i])) {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("store: migration %s: missing version prefix", name)
	}
	n, err := strconv.Atoi(name[:i])
	if err != nil {
		return 0, fmt.Errorf("store: migration %s: %w", name, err)
	}
	return n, nil
}

func applyRAGMigration(ctx context.Context, tx pgx.Tx, body, vectorExt string) error {
	if vectorExt == "" {
		vectorExt = "vector"
	}
	if _, err := tx.Exec(ctx, "SAVEPOINT rag_ext"); err != nil {
		return fmt.Errorf("store: rag savepoint: %w", err)
	}
	_, err := tx.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS "+pgx.Identifier{vectorExt}.Sanitize()+" WITH SCHEMA public")
	if err != nil {
		if _, rb := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT rag_ext"); rb != nil {
			return fmt.Errorf("store: rag rollback: %w", rb)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO meta (key, value) VALUES ('rag', 'unavailable')
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`); err != nil {
			return fmt.Errorf("store: rag meta: %w", err)
		}
		return nil
	}
	if _, err := tx.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS unaccent WITH SCHEMA public"); err != nil {
		return fmt.Errorf("store: unaccent: %w", err)
	}
	sql := strings.ReplaceAll(body, "__DIM__", strconv.Itoa(EmbeddingDim))
	for _, stmt := range splitSQL(sql) {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("store: apply 0007_rag.sql: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO meta (key, value) VALUES ('rag', 'ready')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`); err != nil {
		return fmt.Errorf("store: rag ready: %w", err)
	}
	return nil
}

func splitSQL(s string) []string {
	var stmts []string
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		stmts = append(stmts, part)
	}
	return stmts
}
