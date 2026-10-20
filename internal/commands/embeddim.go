package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/SniksaX/Konsolai/internal/embed"
	"github.com/SniksaX/Konsolai/internal/rag"
	"github.com/SniksaX/Konsolai/internal/store"
)

func checkEmbedDims(ctx context.Context, env *Env) doctorCheck {
	model, index, state, err := embedIndexState(ctx, env)
	switch state {
	case embedOff:
		return doctorCheck{mark: "!", name: "embed dims", detail: "not configured"}
	case embedMatch:
		return doctorCheck{mark: "✓", name: "embed dims", detail: "ok"}
	case embedMismatch:
		return doctorCheck{mark: "!", name: "embed dims", detail: rag.MismatchLine(model, index)}
	default:
		detail := "unreachable"
		fix := "roles.embed"
		if err != nil && strings.Contains(err.Error(), "embedding dim") {
			detail = "index unknown"
			fix = "database.url"
		}
		return doctorCheck{mark: "!", name: "embed dims", detail: detail, fix: fix}
	}
}

func writeEmbedDims(ctx context.Context, env *Env) {
	model, index, state, _ := embedIndexState(ctx, env)
	if state != embedMatch && state != embedMismatch {
		return
	}
	fmt.Fprintf(env.out(), "embed dims     %d (index %d)\n", model, index)
}

type embedState int

const (
	embedOff embedState = iota
	embedMatch
	embedMismatch
	embedUnknown
)

func embedIndexState(ctx context.Context, env *Env) (model, index int, state embedState, err error) {
	if env == nil || !env.Profile.RAGEnabled() || strings.TrimSpace(env.Profile.EmbedURL()) == "" {
		return 0, 0, embedOff, nil
	}
	if m, n, decided, disabled := rag.Snapshot(); decided {
		if disabled {
			return m, n, embedMismatch, nil
		}
		return m, n, embedMatch, nil
	}
	var st *store.Store
	if env.Mem != nil {
		st = env.Mem.Store()
	}
	if st == nil {
		return 0, 0, embedOff, nil
	}
	model, err = probeEmbedDim(ctx, env)
	if err != nil {
		return 0, 0, embedUnknown, err
	}
	index, err = st.EmbeddingDim(ctx)
	if err != nil {
		return model, 0, embedUnknown, err
	}
	rag.Note(model, index)
	if model == index {
		return model, index, embedMatch, nil
	}
	return model, index, embedMismatch, nil
}

func probeEmbedDim(ctx context.Context, env *Env) (int, error) {
	if env == nil {
		return 0, errors.New("commands: embed dim: no env")
	}
	emb := env.Embed
	if emb == nil {
		url := strings.TrimSpace(env.Profile.EmbedURL())
		if url == "" {
			return 0, errors.New("commands: embed dim: not configured")
		}
		emb = embed.New(url, "")
		env.Embed = emb
	}
	if d := emb.Dim(); d > 0 {
		return d, nil
	}
	vecs, err := emb.Embed(ctx, []string{"dim"})
	if err != nil {
		return 0, fmt.Errorf("commands: embed dim: %w", err)
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return 0, errors.New("commands: embed dim: empty vector")
	}
	return len(vecs[0]), nil
}

func resetRAGDim(ctx context.Context, env *Env, args []string) int {
	if env == nil {
		return 1
	}
	yes := env.Yes
	for _, a := range args {
		if a == "--yes" {
			yes = true
			continue
		}
		fmt.Fprintln(env.errw(), "usage: /set rag reset-dim [--yes]")
		return 2
	}
	var st *store.Store
	if env.Mem != nil {
		st = env.Mem.Store()
	}
	if st == nil {
		fmt.Fprintln(env.errw(), "rag reset-dim needs the database (memory is not configured)")
		return 1
	}
	dim, err := probeEmbedDim(ctx, env)
	if err != nil {
		fmt.Fprintln(env.errw(), err)
		return 1
	}
	if !yes {
		summary := fmt.Sprintf("This drops and recreates memory_chunks and memory_files at the current model's dimension (%d). Indexed data is lost.", dim)
		ok, cerr := confirmReset(env, summary)
		if cerr != nil {
			fmt.Fprintln(env.errw(), cerr)
			return 1
		}
		if !ok {
			fmt.Fprintln(env.out(), "left unchanged")
			return 1
		}
	}
	if err := st.ResetEmbeddingDim(ctx, dim); err != nil {
		fmt.Fprintln(env.errw(), err)
		return 1
	}
	rag.ResetDims()
	fmt.Fprintf(env.out(), "recreated memory_chunks and memory_files at %d dims\n", dim)
	return 0
}
