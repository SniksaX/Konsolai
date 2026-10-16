package store

import (
	"context"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
)

func unitVec(i int) []float32 {
	v := make([]float32, EmbeddingDim)
	v[i%EmbeddingDim] = 1
	return v
}

func TestRAGMigrationDedupeAndTSV(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if !s.RAGAvailable(ctx) {
		t.Fatal("want rag ready")
	}
	pid, err := s.UpsertProject(ctx, "/tmp/rag", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	in := ChunkIn{
		Profile:   "home",
		ProjectID: &pid,
		Source:    "turn",
		Ref:       json.RawMessage(`{"title":"t"}`),
		Content:   "hello unique-rag-phrase world",
		Embedding: unitVec(0),
	}
	ok, err := s.InsertChunk(ctx, in)
	if err != nil || !ok {
		t.Fatalf("insert %v %v", ok, err)
	}
	ok, err = s.InsertChunk(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("duplicate should be skipped")
	}
	var hit bool
	err = s.pool.QueryRow(ctx, `
		SELECT tsv @@ to_tsquery('simple', 'hello')
		FROM memory_chunks WHERE content_hash = $1`, ContentHash(in.Content)).Scan(&hit)
	if err != nil {
		t.Fatal(err)
	}
	if !hit {
		t.Fatal("generated tsv missed hello")
	}
}

func TestRAGUnavailableWithoutVector(t *testing.T) {
	s := openSchema(t)
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version int PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			t.Fatal(err)
		}
		if version >= 7 {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		for _, stmt := range splitSQL(string(body)) {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version); err != nil {
			t.Fatal(err)
		}
	}
	body, err := migrationFS.ReadFile("migrations/0007_rag.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := applyRAGMigration(ctx, tx, string(body), "konsolai_missing_vector"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	v, err := s.Meta(ctx, "rag")
	if err != nil || v != "unavailable" {
		t.Fatalf("meta %q %v", v, err)
	}
	if s.RAGAvailable(ctx) {
		t.Fatal("rag should be off")
	}
	_, err = s.pool.Exec(ctx, "SELECT 1 FROM memory_chunks")
	if err == nil {
		t.Fatal("memory_chunks should not exist")
	}
	if _, err := s.UpsertProject(ctx, "/tmp/still-works", nil, nil); err != nil {
		t.Fatalf("store should still work: %v", err)
	}
}
