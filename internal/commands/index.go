package commands

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/SniksaX/Konsolai/internal/rag"
	"github.com/SniksaX/Konsolai/internal/repo"
	"github.com/SniksaX/Konsolai/internal/store"
	"github.com/SniksaX/Konsolai/internal/ui"
)

func runIndex(ctx context.Context, env *Env, args []string) int {
	if len(args) == 1 && args[0] == "status" {
		return indexStatus(ctx, env)
	}
	if len(args) == 1 && args[0] == "clear" {
		return indexClear(ctx, env)
	}
	if len(args) > 1 {
		fmt.Fprintln(env.errw(), `usage: /index [path] | /index status | /index clear`)
		return 2
	}
	st, pid, ok := needIndex(ctx, env)
	if !ok {
		return 1
	}
	if env.Embed == nil {
		fmt.Fprintln(env.errw(), "rag is not configured")
		return 1
	}
	root := repo.Root(env.Cwd)
	filter := ""
	if len(args) == 1 {
		p := args[0]
		if !filepath.IsAbs(p) {
			p = filepath.Join(env.Cwd, p)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			fmt.Fprintln(env.errw(), "path is outside the project")
			return 1
		}
		filter = rel
	}
	live := ui.StartLive("indexing")
	hooks := rag.IndexHooks{
		Yes: env.Yes,
		Progress: func(done, total int, eta time.Duration) {
			label := fmt.Sprintf("indexing %d/%d files", done, total)
			if eta > 0 {
				label += fmt.Sprintf(" ~%s left", formatIndexDur(eta))
			}
			live.SetLabel(label)
		},
		Confirm: func(n int, eta time.Duration) bool {
			live.Stop()
			msg := fmt.Sprintf("Indexing ~%d files will take about %s (embedding is slow). Continue? [y/N]", n, formatIndexDur(eta))
			if env.Confirm != nil {
				d, _, err := env.Confirm.Confirm(msg, "")
				live = ui.StartLive("indexing")
				return err == nil && (d == ui.Approve || d == ui.ApproveAll)
			}
			fmt.Fprintln(env.out(), msg)
			live = ui.StartLive("indexing")
			return false
		},
	}
	stats, err := rag.IndexTune(ctx, st, env.Embed, env.Profile.Name, pid, root, filter, rag.TuneFrom(env.Profile), hooks)
	live.Stop()
	if err != nil {
		fmt.Fprintln(env.errw(), "konsolai:", err)
		return 1
	}
	fmt.Fprintf(env.out(), "indexed %d files (%d chunks, %d unchanged) in %s\n",
		stats.Indexed, stats.Chunks, stats.Unchanged, formatIndexDur(stats.Duration))
	if stats.Orphans > 0 {
		fmt.Fprintf(env.out(), "repaired %d memory_files rows with zero chunks\n", stats.Orphans)
	}
	if filter != "" && stats.Considered == 0 {
		fmt.Fprintf(env.errw(), "no files matched %s\n", filter)
		return 1
	}
	if stats.Failed > 0 {
		fmt.Fprintf(env.errw(), "failed %d files\n", stats.Failed)
		if stats.LastError != nil {
			fmt.Fprintln(env.errw(), stats.LastError)
		}
	}
	return 0
}

func indexStatus(ctx context.Context, env *Env) int {
	st, pid, ok := needIndex(ctx, env)
	if !ok {
		return 1
	}
	files, err := st.ListMemoryFiles(ctx, pid)
	if err != nil {
		fmt.Fprintln(env.errw(), "konsolai:", err)
		return 1
	}
	counts, err := st.ChunkCounts(ctx, env.Profile.Name, &pid)
	if err != nil {
		fmt.Fprintln(env.errw(), "konsolai:", err)
		return 1
	}
	last, has, err := st.LastIndexedAt(ctx, pid)
	if err != nil {
		fmt.Fprintln(env.errw(), "konsolai:", err)
		return 1
	}
	fmt.Fprintf(env.out(), "files          %d\n", len(files))
	fmt.Fprintf(env.out(), "chunks         %d\n", counts["code"])
	if has {
		fmt.Fprintf(env.out(), "last indexed   %s\n", last.Local().Format("2006-01-02 15:04:05"))
	} else {
		fmt.Fprintln(env.out(), "last indexed   never")
	}
	return 0
}

func indexClear(ctx context.Context, env *Env) int {
	st, pid, ok := needIndex(ctx, env)
	if !ok {
		return 1
	}
	if err := st.ClearCodeIndex(ctx, pid); err != nil {
		fmt.Fprintln(env.errw(), "konsolai:", err)
		return 1
	}
	fmt.Fprintln(env.out(), "index cleared")
	return 0
}

func needIndex(ctx context.Context, env *Env) (*store.Store, int64, bool) {
	if env == nil || env.Mem == nil || env.Mem.Store() == nil {
		fmt.Fprintln(env.errw(), "memory is not configured (see ~/.config/konsolai/config.yaml)")
		return nil, 0, false
	}
	st := env.Mem.Store()
	if !st.RAGAvailable(ctx) {
		fmt.Fprintln(env.errw(), "rag unavailable")
		return nil, 0, false
	}
	pid := env.ProjectID
	if pid == nil {
		pid = projectIDOf(ctx, env)
	}
	if pid == nil {
		fmt.Fprintln(env.errw(), "not in a project")
		return nil, 0, false
	}
	return st, *pid, true
}

func formatIndexDur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
