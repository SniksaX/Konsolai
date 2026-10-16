package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/siraj/konsolai/internal/memory"
	"github.com/siraj/konsolai/internal/turn"
	"github.com/siraj/konsolai/internal/ui"
)

func runHistory(ctx context.Context, env *Env, args []string) int {
	n, errMsg := parseN(args, 10, 50)
	if errMsg != "" {
		fmt.Fprintln(env.errw(), errMsg)
		return 2
	}
	items := loadHistoryFixed(ctx, env, n)
	if len(items) == 0 {
		fmt.Fprintln(env.out(), "no sessions")
		return 0
	}
	for i, it := range items {
		fmt.Fprintf(env.out(), "%d  %s  %s  %s  %s  %s\n",
			i+1, ageLabel(it.Age), orDash(it.Mode), orDash(it.Status), clipTitle(it.Title), orDash(it.CWD))
	}
	return 0
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func runShow(ctx context.Context, env *Env, args []string) int {
	n, errMsg := parseN(args, 0, 50)
	if errMsg != "" || n < 1 {
		fmt.Fprintln(env.errw(), "usage: /show <n>")
		return 2
	}
	items := loadHistoryFixed(ctx, env, 50)
	if n > len(items) {
		fmt.Fprintln(env.errw(), "no such session")
		return 1
	}
	pairs := pairsFromMsgs(items[n-1].Msgs)
	rend := ui.NewRenderer(env.out(), env.Plain)
	for _, p := range pairs {
		_, _ = rend.Write([]byte("### Prompt\n\n" + p.Prompt + "\n\n### Answer\n\n" + p.Answer + "\n\n"))
	}
	_ = rend.Flush()
	return 0
}

func runResume(ctx context.Context, env *Env, args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(env.errw(), "usage: /resume <n> \"<request>\"")
		return 2
	}
	n, errMsg := parseN(args[:1], 0, 50)
	if errMsg != "" || n < 1 {
		fmt.Fprintln(env.errw(), "usage: /resume <n> \"<request>\"")
		return 2
	}
	req := strings.TrimSpace(args[1])
	if req == "" {
		fmt.Fprintln(env.errw(), "usage: /resume <n> \"<request>\"")
		return 2
	}
	items := loadHistoryFixed(ctx, env, 50)
	if n > len(items) {
		fmt.Fprintln(env.errw(), "no such session")
		return 1
	}
	it := items[n-1]
	tReq := turn.Request{
		Text: req, Yes: env.Yes, Cwd: env.Cwd, ResumeSession: it.ID, Plain: env.Plain,
	}
	if env.RunTurn != nil {
		return env.RunTurn(ctx, tReq).ExitCode
	}
	if env.Runner == nil {
		fmt.Fprintln(env.errw(), "konsolai: runner not available")
		return 1
	}
	res := env.Runner.RunTurn(ctx, tReq)
	return res.ExitCode
}

func runNew(ctx context.Context, env *Env, _ []string) int {
	if env.CloseAll != nil {
		if err := env.CloseAll(ctx); err != nil {
			fmt.Fprintln(env.errw(), "konsolai:", err)
			return 1
		}
	} else if env.Mem != nil {
		if s := env.Mem.Store(); s != nil {
			pid := projectIDOf(ctx, env)
			if _, err := s.CloseSessions(ctx, env.Profile.Name, pid, env.Cwd); err != nil {
				fmt.Fprintln(env.errw(), "konsolai:", err)
				return 1
			}
		}
	}
	_ = memory.ClearLast()
	fmt.Fprintln(env.out(), "next request starts a new session")
	return 0
}

func runDiff(ctx context.Context, env *Env, args []string) int {
	n := 1
	if len(args) > 0 {
		var errMsg string
		n, errMsg = parseN(args, 1, 50)
		if errMsg != "" {
			fmt.Fprintln(env.errw(), errMsg)
			return 2
		}
	}
	items := loadHistoryFixed(ctx, env, 50)
	if n > len(items) || len(items) == 0 {
		fmt.Fprintln(env.errw(), "no such session")
		return 1
	}
	it := items[n-1]
	chs := it.Changes
	if len(chs) == 0 && it.ID != "" && env.Mem != nil && env.Mem.Store() != nil {
		rows, err := env.Mem.Store().ListFileChanges(ctx, it.ID)
		if err != nil {
			fmt.Fprintln(env.errw(), "konsolai:", err)
			return 1
		}
		for _, c := range rows {
			chs = append(chs, fileChange{Path: c.Path, Diff: c.Diff})
		}
	}
	if len(chs) == 0 {
		fmt.Fprintln(env.out(), "no file changes")
		return 0
	}
	for _, c := range chs {
		fmt.Fprintln(env.out(), c.Path)
		if c.Diff != "" {
			fmt.Fprintln(env.out(), ui.ColorDiff(c.Diff))
		}
	}
	return 0
}
