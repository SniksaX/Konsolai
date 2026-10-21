package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/SniksaX/Konsolai/internal/memory"
	"github.com/SniksaX/Konsolai/internal/turn"
	"github.com/SniksaX/Konsolai/internal/ui"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
)

func runHistory(ctx context.Context, env *Env, args []string) int {
	n, here, errMsg := parseHistoryArgs(args)
	if errMsg != "" {
		fmt.Fprintln(env.errw(), errMsg)
		return 2
	}
	items := loadHistory(ctx, env, n, here)
	if len(items) == 0 {
		fmt.Fprintln(env.out(), "no sessions")
		return 0
	}
	rows := make([][5]string, len(items))
	var widths [5]int
	for i, it := range items {
		rows[i] = [5]string{strconv.Itoa(i + 1), ageLabel(it.Age), orDash(it.Mode), orDash(it.Status), clipTitle(it.Title)}
		for j, cell := range rows[i] {
			widths[j] = max(widths[j], theme.DisplayWidth(cell))
		}
	}
	p := ui.Current()
	for i, it := range items {
		r := rows[i]
		cwd := orDash(it.CWD)
		if it.Other {
			cwd = p.Muted(cwd)
		}
		fmt.Fprintf(env.out(), "%s  %s  %s  %s  %s  %s\n",
			padCell(r[0], widths[0], true), p.Muted(padCell(r[1], widths[1], false)),
			p.Accent(padCell(r[2], widths[2], false)), statusCell(r[3], widths[3]),
			padCell(r[4], widths[4], false), cwd)
	}
	return 0
}

// statusCell pads a session status and colours it by outcome.
func statusCell(status string, width int) string {
	cell := padCell(status, width, false)
	p := ui.Current()
	switch status {
	case "failed":
		return p.Error(cell)
	case "aborted":
		return p.Warn(cell)
	case "finished":
		return p.Muted(cell)
	}
	return cell
}

func parseHistoryArgs(args []string) (int, bool, string) {
	n := 10
	here := false
	seenN := false
	for _, a := range args {
		if a == "--here" || a == "here" {
			here = true
			continue
		}
		if seenN {
			return 0, false, "usage: /history [n] [--here]"
		}
		v, errMsg := parseN([]string{a}, 10, 50)
		if errMsg != "" {
			return 0, false, errMsg
		}
		n = v
		seenN = true
	}
	return n, here, ""
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
	items := loadHistory(ctx, env, 50, false)
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
	items := loadHistory(ctx, env, 50, false)
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
	if env.InRepl {
		if env.OnNewSession != nil {
			env.OnNewSession()
		}
		fmt.Fprintln(env.out(), "next request starts a new session")
		return 0
	}
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
	items := loadHistory(ctx, env, 50, false)
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
