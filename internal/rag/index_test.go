package rag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/siraj/konsolai/internal/store"
)

func TestChunkGoBoundaries(t *testing.T) {
	src := `package p

import "fmt"

// Foo greets.
func Foo() {
	fmt.Println("hi")
}

type Bar struct {
	N int
}

func (b *Bar) Q() int { return b.N }
`
	chunks := ChunkGo("p.go", src)
	if len(chunks) < 3 {
		t.Fatalf("got %d %+v", len(chunks), chunks)
	}
	var foo, bar, meth bool
	for _, c := range chunks {
		if c.Symbol == "Foo" {
			foo = true
			if !strings.Contains(c.Content, "// Foo greets.") || !strings.Contains(c.Content, "func Foo()") {
				t.Fatalf("foo chunk missing doc: %q", c.Content)
			}
			if c.StartLine < 1 || c.EndLine < c.StartLine {
				t.Fatalf("foo lines %d-%d", c.StartLine, c.EndLine)
			}
		}
		if c.Symbol == "Bar" {
			bar = true
			if !strings.Contains(c.Content, "type Bar struct") {
				t.Fatalf("bar %q", c.Content)
			}
		}
		if strings.Contains(c.Symbol, "Q") {
			meth = true
		}
	}
	if !foo || !bar || !meth {
		t.Fatalf("symbols foo=%v bar=%v meth=%v %+v", foo, bar, meth, chunks)
	}
	var body strings.Builder
	body.WriteString("package p\n\nfunc Long() {\n")
	for i := 0; i < 250; i++ {
		body.WriteString("\t_ = 1\n")
	}
	body.WriteString("}\n")
	long := ChunkGo("long.go", body.String())
	if len(long) < 2 {
		t.Fatalf("long func should window, got %d", len(long))
	}
	for _, c := range long {
		n := c.EndLine - c.StartLine + 1
		if n > goWinLines+1 {
			t.Fatalf("window %d lines", n)
		}
	}
}

type idxStore struct {
	memStore
	files map[string]string
}

func (s *idxStore) DeleteCodeChunks(_ context.Context, _ int64, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var keep []store.ChunkIn
	for _, r := range s.rows {
		var m map[string]any
		_ = json.Unmarshal(r.Ref, &m)
		if p, _ := m["path"].(string); p == path {
			continue
		}
		keep = append(keep, r)
	}
	s.rows = keep
	s.hashes = map[string]struct{}{}
	for _, r := range keep {
		s.hashes[store.ContentHash(r.Content)] = struct{}{}
	}
	return nil
}

func (s *idxStore) UpsertMemoryFile(_ context.Context, _ int64, path, sha string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.files == nil {
		s.files = map[string]string{}
	}
	s.files[path] = sha
	return nil
}

func (s *idxStore) MemoryFileSHA(_ context.Context, _ int64, path string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sha, ok := s.files[path]
	return sha, ok, nil
}

func (s *idxStore) ListMemoryFiles(_ context.Context, _ int64) ([]store.MemoryFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.MemoryFile
	for p, sha := range s.files {
		out = append(out, store.MemoryFile{Path: p, SHA256: sha})
	}
	return out, nil
}

func (s *idxStore) DeleteMemoryFile(_ context.Context, _ int64, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, path)
	return nil
}

func (s *idxStore) ClearCodeIndex(context.Context, int64) error { return nil }
func (s *idxStore) ChunkCounts(context.Context, string, *int64) (map[string]int, error) {
	return map[string]int{}, nil
}
func (s *idxStore) LastIndexedAt(context.Context, int64) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func TestIndexIncrementalSkipChangeDelete(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("keep.go", "package p\n\nfunc Keep() {}\n")
	mustWrite("gone.go", "package p\n\nfunc Gone() {}\n")
	st := &idxStore{memStore: memStore{avail: true}}
	emb := &fakeEmbed{}
	stats, err := Index(context.Background(), st, emb, "home", 1, root, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Indexed != 2 || stats.Unchanged != 0 {
		t.Fatalf("first %+v", stats)
	}
	stats, err = Index(context.Background(), st, emb, "home", 1, root, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Unchanged != 2 || stats.Indexed != 0 {
		t.Fatalf("skip %+v", stats)
	}
	mustWrite("keep.go", "package p\n\nfunc Keep() { _ = 1 }\n")
	if err := os.Remove(filepath.Join(root, "gone.go")); err != nil {
		t.Fatal(err)
	}
	stats, err = Index(context.Background(), st, emb, "home", 1, root, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Indexed != 1 {
		t.Fatalf("changed indexed %+v", stats)
	}
	if stats.Deleted != 1 {
		t.Fatalf("deleted %+v files=%v", stats, st.files)
	}
	if _, ok := st.files["gone.go"]; ok {
		t.Fatal("gone.go still in memory_files")
	}
	sum := sha256.Sum256([]byte("package p\n\nfunc Keep() { _ = 1 }\n"))
	if st.files["keep.go"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha %s", st.files["keep.go"])
	}
}
