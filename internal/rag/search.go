package rag

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/SniksaX/Konsolai/internal/embed"
	"github.com/SniksaX/Konsolai/internal/store"
)

const (
	rrfK        = 60
	vectorTop   = 30
	textTop     = 30
	rerankTop   = 20
	defaultK    = 6
	maxK        = 10
	rerankFloor = 0.1
	contentCap  = 1200
	memoryNote  = "Retrieved from past sessions; may be outdated."
)

var _ SearchBackend = (*store.Store)(nil)

type SearchBackend interface {
	SearchVector(ctx context.Context, profile string, projectID *int64, scope string, vec []float32, limit int) ([]store.SearchHit, error)
	SearchText(ctx context.Context, profile string, projectID *int64, scope, query string, limit int) ([]store.SearchHit, error)
	RAGAvailable(ctx context.Context) bool
}

type Reranker interface {
	Rerank(ctx context.Context, query string, texts []string) ([]embed.Scored, error)
}

type Hit struct {
	ID      int64
	Source  string
	Ref     json.RawMessage
	Content string
	Score   float64
}

func Search(ctx context.Context, st SearchBackend, emb Embedder, rerank Reranker, profile string, projectID *int64, query, scope string, k int) ([]Hit, error) {
	return SearchTune(ctx, st, emb, rerank, profile, projectID, query, scope, k, Tune{})
}

func SearchTune(ctx context.Context, st SearchBackend, emb Embedder, rerank Reranker, profile string, projectID *int64, query, scope string, k int, tune Tune) ([]Hit, error) {
	if st == nil || emb == nil || !st.RAGAvailable(ctx) || ragBlocked(nil) {
		return nil, nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		scope = "all"
	}
	vt, tt, rk, top, defK, floor := tune.search()
	if k < 1 {
		k = defK
	}
	if k > maxK {
		k = maxK
	}
	vecs, err := emb.Embed(ctx, []string{query})
	if err != nil || len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil, err
	}
	if !acceptDims(ctx, searchCatalog(st), len(vecs[0]), nil) {
		return nil, nil
	}
	vecHits, err := st.SearchVector(ctx, profile, projectID, scope, vecs[0], vt)
	if err != nil {
		return nil, err
	}
	txtHits, err := st.SearchText(ctx, profile, projectID, scope, query, tt)
	if err != nil {
		return nil, err
	}
	merged := mergeRRF(vecHits, txtHits, rk)
	if len(merged) > top {
		merged = merged[:top]
	}
	return applyRerankFloor(ctx, rerank, query, merged, k, floor), nil
}

func mergeRRF(vec, txt []store.SearchHit, k int) []Hit {
	scores := map[int64]float64{}
	byID := map[int64]store.SearchHit{}
	add := func(hits []store.SearchHit) {
		for i, h := range hits {
			scores[h.ID] += 1.0 / float64(k+i+1)
			byID[h.ID] = h
		}
	}
	add(vec)
	add(txt)
	ids := make([]int64, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if scores[ids[i]] == scores[ids[j]] {
			return ids[i] < ids[j]
		}
		return scores[ids[i]] > scores[ids[j]]
	})
	out := make([]Hit, 0, len(ids))
	for _, id := range ids {
		h := byID[id]
		out = append(out, Hit{ID: h.ID, Source: h.Source, Ref: h.Ref, Content: h.Content, Score: scores[id]})
	}
	return out
}

func applyRerank(ctx context.Context, r Reranker, query string, hits []Hit, k int) []Hit {
	return applyRerankFloor(ctx, r, query, hits, k, rerankFloor)
}

func applyRerankFloor(ctx context.Context, r Reranker, query string, hits []Hit, k int, floor float64) []Hit {
	if len(hits) == 0 {
		return hits
	}
	if r == nil {
		return capK(hits, k)
	}
	texts := make([]string, len(hits))
	for i, h := range hits {
		texts[i] = h.Content
	}
	scored, err := r.Rerank(ctx, query, texts)
	if err != nil {
		return capK(hits, k)
	}
	var out []Hit
	for _, s := range scored {
		if s.Index < 0 || s.Index >= len(hits) {
			continue
		}
		if floor <= 0 {
			floor = rerankFloor
		}
		if s.Score < floor {
			continue
		}
		h := hits[s.Index]
		h.Score = s.Score
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return capK(out, k)
}

func capK(hits []Hit, k int) []Hit {
	if k < len(hits) {
		return hits[:k]
	}
	return hits
}

func SourceLine(h Hit) string {
	var ref map[string]any
	_ = json.Unmarshal(h.Ref, &ref)
	str := func(k string) string {
		v, _ := ref[k].(string)
		return v
	}
	switch h.Source {
	case "code":
		path := str("path")
		sym := str("symbol")
		start, end := asInt(ref["start_line"]), asInt(ref["end_line"])
		line := path
		if start > 0 {
			endAt := end
			if endAt < start {
				endAt = start
			}
			line += ":" + strconv.Itoa(start) + "-" + strconv.Itoa(endAt)
		}
		if sym != "" {
			line += " " + sym
		}
		return strings.TrimSpace(line)
	case "web":
		if u := str("url"); u != "" {
			if t := str("title"); t != "" {
				return t + " " + u
			}
			return u
		}
		return str("title")
	default:
		title := str("title")
		date := str("date")
		switch {
		case title != "" && date != "":
			return title + " · " + date
		case title != "":
			return title
		case date != "":
			return date
		default:
			return h.Source
		}
	}
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

func FormatHits(hits []Hit) string {
	if len(hits) == 0 {
		return "no results"
	}
	var b strings.Builder
	for i, h := range hits {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(". ")
		b.WriteString(SourceLine(h))
		b.WriteByte('\n')
		b.WriteString(clipRunes(h.Content, contentCap))
		if !strings.HasSuffix(h.Content, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func clipRunes(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func PrependMemory(user string, hits []Hit) string {
	if len(hits) == 0 {
		return user
	}
	var b strings.Builder
	b.WriteString("<memory>\n")
	b.WriteString(memoryNote)
	b.WriteByte('\n')
	b.WriteString(FormatHits(hits))
	b.WriteString("</memory>\n\n")
	b.WriteString(user)
	return b.String()
}
