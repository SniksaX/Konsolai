package rag

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/siraj/konsolai/internal/redact"
	"github.com/siraj/konsolai/internal/repo"
	"github.com/siraj/konsolai/internal/store"
)

const (
	maxIndexBytes = 512 << 10
	goSplitLines  = 200
	goWinLines    = 120
	goOverlap     = 20
	textWinLines  = 60
	textOverlap   = 10
)

var _ IndexBackend = (*store.Store)(nil)

type IndexBackend interface {
	ChunkStore
	DeleteCodeChunks(ctx context.Context, projectID int64, path string) error
	UpsertMemoryFile(ctx context.Context, projectID int64, path, sha string) error
	MemoryFileSHA(ctx context.Context, projectID int64, path string) (string, bool, error)
	ListMemoryFiles(ctx context.Context, projectID int64) ([]store.MemoryFile, error)
	DeleteMemoryFile(ctx context.Context, projectID int64, path string) error
	ClearCodeIndex(ctx context.Context, projectID int64) error
	ChunkCounts(ctx context.Context, profile string, projectID *int64) (map[string]int, error)
	LastIndexedAt(ctx context.Context, projectID int64) (time.Time, bool, error)
	OrphanMemoryFiles(ctx context.Context, projectID int64) ([]string, error)
	PersistCodeFile(ctx context.Context, profile string, projectID int64, path, sha string, chunks []store.ChunkIn) (int, error)
}

type CodeChunk struct {
	Content   string
	Path      string
	StartLine int
	EndLine   int
	Symbol    string
}

type IndexStats struct {
	Indexed    int
	Chunks     int
	Unchanged  int
	Deleted    int
	Considered int
	Failed     int
	Orphans    int
	LastError  error
	Duration   time.Duration
}

type IndexHooks struct {
	Progress     func(done, total int, eta time.Duration)
	Confirm      func(files int, eta time.Duration) bool
	Yes          bool
	ConfirmAfter time.Duration
}

func Index(ctx context.Context, st IndexBackend, emb Embedder, profile string, projectID int64, root, filter string, progress func(done, total int)) (IndexStats, error) {
	h := IndexHooks{}
	if progress != nil {
		h.Progress = func(done, total int, _ time.Duration) { progress(done, total) }
	}
	return IndexTune(ctx, st, emb, profile, projectID, root, filter, Tune{}, h)
}

func IndexTune(ctx context.Context, st IndexBackend, emb Embedder, profile string, projectID int64, root, filter string, tune Tune, hooks IndexHooks) (IndexStats, error) {
	var stats IndexStats
	start := time.Now()
	if st == nil || emb == nil || !st.RAGAvailable(ctx) {
		stats.Duration = time.Since(start)
		return stats, nil
	}
	files, err := repo.Files(ctx, root)
	if err != nil {
		return stats, err
	}
	filter = filepath.ToSlash(strings.TrimPrefix(filter, "./"))
	var todo []string
	for _, rel := range files {
		if !pathInFilter(rel, filter) {
			continue
		}
		if redact.SensitivePath(rel) {
			continue
		}
		todo = append(todo, rel)
	}
	stats.Considered = len(todo)
	orphanSet := map[string]struct{}{}
	if orphans, err := st.OrphanMemoryFiles(ctx, projectID); err == nil {
		stats.Orphans = len(orphans)
		for _, p := range orphans {
			orphanSet[p] = struct{}{}
		}
	}
	seen := map[string]struct{}{}
	var perFile time.Duration
	timed := false
	asked := false
	maxBytes, _, _, _, _, _ := tune.index()
	limitMin := pickInt(tune.Index.ConfirmOverMinutes, 5)
	for i, rel := range todo {
		if hooks.Progress != nil {
			eta := time.Duration(0)
			if perFile > 0 {
				eta = perFile * time.Duration(len(todo)-i)
			}
			hooks.Progress(i, len(todo), eta)
		}
		if err := ctx.Err(); err != nil {
			stats.Duration = time.Since(start)
			return stats, err
		}
		seen[rel] = struct{}{}
		abs := filepath.Join(root, rel)
		fi, err := os.Stat(abs)
		if err != nil || fi.IsDir() {
			continue
		}
		if fi.Size() > maxBytes {
			continue
		}
		body, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		if bytes.IndexByte(body, 0) >= 0 {
			continue
		}
		sum := sha256.Sum256(body)
		sha := hex.EncodeToString(sum[:])
		old, ok, err := st.MemoryFileSHA(ctx, projectID, rel)
		_, orphan := orphanSet[rel]
		if err == nil && ok && old == sha && !orphan {
			stats.Unchanged++
			continue
		}
		chunks := ChunkFileTune(rel, string(body), tune)
		texts := make([]string, len(chunks))
		for i, c := range chunks {
			texts[i] = c.Content
		}
		t0 := time.Now()
		vecs, err := emb.Embed(ctx, texts)
		took := time.Since(t0)
		if !timed {
			timed = true
			perFile = took
			eta := perFile * time.Duration(len(todo)-i)
			limit := time.Duration(limitMin) * time.Minute
			if hooks.ConfirmAfter > 0 {
				limit = hooks.ConfirmAfter
			}
			if !asked && !hooks.Yes && eta > limit && hooks.Confirm != nil {
				asked = true
				if !hooks.Confirm(len(todo)-i, eta) {
					stats.Duration = time.Since(start)
					return stats, nil
				}
			}
		} else if took > 0 {
			perFile = (perFile + took) / 2
		}
		if err != nil {
			stats.Failed++
			stats.LastError = err
			continue
		}
		if len(vecs) != len(chunks) {
			stats.Failed++
			stats.LastError = fmt.Errorf("embed: got %d vectors for %d chunks", len(vecs), len(chunks))
			continue
		}
		ins := make([]store.ChunkIn, len(chunks))
		for i, c := range chunks {
			ref, _ := json.Marshal(map[string]any{
				"path":       c.Path,
				"start_line": c.StartLine,
				"end_line":   c.EndLine,
				"symbol":     c.Symbol,
			})
			ins[i] = store.ChunkIn{
				Profile:   profile,
				ProjectID: &projectID,
				Source:    "code",
				Ref:       ref,
				Content:   c.Content,
				Embedding: vecs[i],
			}
		}
		n, err := st.PersistCodeFile(ctx, profile, projectID, rel, sha, ins)
		if err != nil {
			stats.Failed++
			stats.LastError = err
			continue
		}
		stats.Indexed++
		stats.Chunks += n
		if hooks.Progress != nil {
			eta := time.Duration(0)
			if perFile > 0 {
				eta = perFile * time.Duration(len(todo)-i-1)
			}
			hooks.Progress(i+1, len(todo), eta)
		}
	}
	if hooks.Progress != nil {
		hooks.Progress(len(todo), len(todo), 0)
	}
	if filter == "" || filter == "." {
		existing, err := st.ListMemoryFiles(ctx, projectID)
		if err == nil {
			for _, f := range existing {
				if _, ok := seen[f.Path]; ok {
					continue
				}
				_ = st.DeleteCodeChunks(ctx, projectID, f.Path)
				_ = st.DeleteMemoryFile(ctx, projectID, f.Path)
				stats.Deleted++
			}
		}
	}
	stats.Duration = time.Since(start)
	return stats, nil
}

func pathInFilter(rel, filter string) bool {
	if filter == "" || filter == "." {
		return true
	}
	filter = strings.TrimSuffix(filepath.ToSlash(filter), "/")
	rel = filepath.ToSlash(rel)
	return rel == filter || strings.HasPrefix(rel, filter+"/")
}

func ChunkFile(path, src string) []CodeChunk {
	return ChunkFileTune(path, src, Tune{})
}

func ChunkFileTune(path, src string, tune Tune) []CodeChunk {
	if strings.EqualFold(filepath.Ext(path), ".go") {
		if chunks := ChunkGoTune(path, src, tune); len(chunks) > 0 {
			return chunks
		}
	}
	return ChunkTextTune(path, src, tune)
}

func ChunkGo(path, src string) []CodeChunk {
	return ChunkGoTune(path, src, Tune{})
}

func ChunkGoTune(path, src string, tune Tune) []CodeChunk {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return ChunkText(path, src)
	}
	_, goSplit, goWin, goOv, _, _ := tune.index()
	lines := strings.Split(src, "\n")
	var out []CodeChunk
	for _, d := range f.Decls {
		switch x := d.(type) {
		case *ast.GenDecl:
			if x.Tok == token.IMPORT {
				continue
			}
			start := fset.Position(x.Pos()).Line
			if x.Doc != nil {
				start = fset.Position(x.Doc.Pos()).Line
			}
			end := fset.Position(x.End()).Line
			sym := genDeclName(x)
			out = append(out, splitDecl(path, lines, start, end, sym, goSplit, goWin, goOv)...)
		case *ast.FuncDecl:
			start := fset.Position(x.Pos()).Line
			if x.Doc != nil {
				start = fset.Position(x.Doc.Pos()).Line
			}
			end := fset.Position(x.End()).Line
			out = append(out, splitDecl(path, lines, start, end, funcName(x), goSplit, goWin, goOv)...)
		}
	}
	return out
}

func funcName(x *ast.FuncDecl) string {
	if x.Recv != nil && len(x.Recv.List) > 0 {
		return "(" + exprName(x.Recv.List[0].Type) + ") " + x.Name.Name
	}
	return x.Name.Name
}

func exprName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprName(t.X)
	case *ast.IndexExpr:
		return exprName(t.X)
	case *ast.IndexListExpr:
		return exprName(t.X)
	default:
		return ""
	}
}

func genDeclName(x *ast.GenDecl) string {
	var names []string
	for _, spec := range x.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			names = append(names, s.Name.Name)
		case *ast.ValueSpec:
			for _, n := range s.Names {
				names = append(names, n.Name)
			}
		}
	}
	return strings.Join(names, ", ")
}

var lineDecl = regexp.MustCompile(`(?i)^\s*(export\s+)?(async\s+)?(default\s+)?((function|class|interface|type|def)\b|const\s+\w+|let\s+\w+|var\s+\w+)`)

func ChunkText(path, src string) []CodeChunk {
	return ChunkTextTune(path, src, Tune{})
}

func ChunkTextTune(path, src string, tune Tune) []CodeChunk {
	_, _, _, _, textWin, textOv := tune.index()
	lines := strings.Split(src, "\n")
	var bounds []int
	for i, line := range lines {
		if lineDecl.MatchString(line) {
			bounds = append(bounds, i+1)
		}
	}
	if len(bounds) == 0 {
		return windowChunks(path, lines, 1, len(lines), "", textWin, textOv)
	}
	var out []CodeChunk
	for i, start := range bounds {
		end := len(lines)
		if i+1 < len(bounds) {
			end = bounds[i+1] - 1
		}
		sym := strings.TrimSpace(lines[start-1])
		if utf8len(sym) > 80 {
			sym = string([]rune(sym)[:80])
		}
		out = append(out, windowChunks(path, lines, start, end, sym, textWin, textOv)...)
	}
	return out
}

func utf8len(s string) int { return len([]rune(s)) }

func splitDecl(path string, lines []string, start, end int, symbol string, split, win, overlap int) []CodeChunk {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if end < start {
		return nil
	}
	if end-start+1 <= split {
		return []CodeChunk{{Content: joinLines(lines, start, end), Path: path, StartLine: start, EndLine: end, Symbol: symbol}}
	}
	return windowChunks(path, lines, start, end, symbol, win, overlap)
}

func windowChunks(path string, lines []string, start, end int, symbol string, win, overlap int) []CodeChunk {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if end < start {
		return nil
	}
	n := end - start + 1
	if n <= win {
		return []CodeChunk{{Content: joinLines(lines, start, end), Path: path, StartLine: start, EndLine: end, Symbol: symbol}}
	}
	step := win - overlap
	if step < 1 {
		step = 1
	}
	var out []CodeChunk
	for s := start; s <= end; s += step {
		e := s + win - 1
		if e > end {
			e = end
		}
		out = append(out, CodeChunk{Content: joinLines(lines, s, e), Path: path, StartLine: s, EndLine: e, Symbol: symbol})
		if e == end {
			break
		}
	}
	return out
}

func joinLines(lines []string, start, end int) string {
	return strings.Join(lines[start-1:end], "\n")
}
