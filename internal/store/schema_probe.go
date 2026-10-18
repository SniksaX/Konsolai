package store

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
)

// LatestVersion is the highest embedded migration prefix.
func LatestVersion() (int, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return 0, fmt.Errorf("store: read migrations: %w", err)
	}
	max := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		n, err := migrationVersion(e.Name())
		if err != nil {
			return 0, err
		}
		if n > max {
			max = n
		}
	}
	return max, nil
}

// HasVector reports whether the pgvector extension is installed.
func (s *Store) HasVector(ctx context.Context) (bool, error) {
	if s == nil || s.pool == nil {
		return false, fmt.Errorf("store: vector extension: no pool")
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector')`).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("store: vector extension: %w", err)
	}
	return ok, nil
}
