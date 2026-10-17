package rag

import "github.com/siraj/konsolai/internal/config"

type Tune struct {
	Chunk  config.RAGChunk
	Search config.RAGSearch
	Index  config.RAGIndex
}

func TuneFrom(c config.Config) Tune {
	return Tune{Chunk: c.RAG.Chunk, Search: c.RAG.Search, Index: c.RAG.Index}
}

func (t Tune) search() (vectorTopN, textTopN, rrf, top, k int, floor float64) {
	b := config.Builtin().RAG.Search
	vectorTopN = pickInt(t.Search.VectorTop, b.VectorTop)
	textTopN = pickInt(t.Search.TextTop, b.TextTop)
	rrf = pickInt(t.Search.RRFK, b.RRFK)
	top = pickInt(t.Search.RerankTop, b.RerankTop)
	k = pickInt(t.Search.K, b.K)
	floor = t.Search.RerankThreshold
	if floor <= 0 {
		floor = b.RerankThreshold
	}
	return
}

func (t Tune) chunk() (turnTok, webTok, webOv int) {
	b := config.Builtin().RAG.Chunk
	return pickInt(t.Chunk.TurnTokens, b.TurnTokens), pickInt(t.Chunk.WebTokens, b.WebTokens), pickInt(t.Chunk.WebOverlapTokens, b.WebOverlapTokens)
}

func (t Tune) index() (maxBytes int64, goSplit, goWin, goOv, textWin, textOv int) {
	b := config.Builtin().RAG
	kb := pickInt(t.Index.MaxFileKB, b.Index.MaxFileKB)
	maxBytes = int64(kb) << 10
	return maxBytes,
		pickInt(t.Chunk.GoMaxDeclLines, b.Chunk.GoMaxDeclLines),
		pickInt(t.Chunk.GoSplitWindowLines, b.Chunk.GoSplitWindowLines),
		pickInt(t.Chunk.GoSplitOverlapLines, b.Chunk.GoSplitOverlapLines),
		pickInt(t.Chunk.CodeWindowLines, b.Chunk.CodeWindowLines),
		pickInt(t.Chunk.CodeOverlapLines, b.Chunk.CodeOverlapLines)
}

func pickInt(n, fallback int) int {
	if n > 0 {
		return n
	}
	return fallback
}
