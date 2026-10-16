package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/siraj/konsolai/internal/rag"
)

func runRecall(ctx context.Context, env *Env, args []string) int {
	query, scope, ok := parseRecallArgs(args)
	if !ok {
		fmt.Fprintln(env.errw(), `usage: /recall "<query>" [--scope sessions|code|web|all]`)
		return 2
	}
	if env.Mem == nil || env.Mem.Store() == nil || env.Embed == nil {
		fmt.Fprintln(env.errw(), "rag is not configured")
		return 1
	}
	st := env.Mem.Store()
	if !st.RAGAvailable(ctx) {
		fmt.Fprintln(env.errw(), "rag unavailable")
		return 1
	}
	hits, err := rag.Search(ctx, st, env.Embed, env.Embed, env.Profile.Name, env.ProjectID, query, scope, 6)
	if err != nil {
		fmt.Fprintln(env.errw(), "konsolai:", err)
		return 1
	}
	out := rag.FormatHits(hits)
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	fmt.Fprint(env.out(), out)
	return 0
}

func parseRecallArgs(args []string) (query, scope string, ok bool) {
	scope = "all"
	var parts []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--scope" {
			if i+1 >= len(args) {
				return "", "", false
			}
			i++
			scope = strings.ToLower(args[i])
			continue
		}
		if strings.HasPrefix(a, "--scope=") {
			scope = strings.ToLower(strings.TrimPrefix(a, "--scope="))
			continue
		}
		parts = append(parts, a)
	}
	query = strings.TrimSpace(strings.Join(parts, " "))
	if query == "" {
		return "", "", false
	}
	switch scope {
	case "sessions", "code", "web", "all":
		return query, scope, true
	default:
		return "", "", false
	}
}
