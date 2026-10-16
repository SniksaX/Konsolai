package commands

import (
	"context"
	"fmt"
	"strings"
)

func runCopy(ctx context.Context, env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(env.errw(), "usage: /copy answer|all|code|cmd [n]")
		return 2
	}
	kind := args[0]
	rest := args[1:]
	items := loadHistoryFixed(ctx, env, 5)
	var pairs []pair
	for i := len(items) - 1; i >= 0; i-- {
		pairs = append(pairs, pairsFromMsgs(items[i].Msgs)...)
	}
	switch kind {
	case "answer":
		n, errMsg := parseN(rest, 1, 5)
		if errMsg != "" {
			fmt.Fprintln(env.errw(), errMsg)
			return 2
		}
		sel := lastN(pairs, n)
		if len(sel) == 0 {
			fmt.Fprintln(env.errw(), "no answers")
			return 1
		}
		var parts []string
		for _, p := range sel {
			parts = append(parts, p.Answer)
		}
		return doCopy(ctx, env, strings.Join(parts, "\n\n---\n\n"), "answer")
	case "all":
		n, errMsg := parseN(rest, 1, 5)
		if errMsg != "" {
			fmt.Fprintln(env.errw(), errMsg)
			return 2
		}
		sel := lastN(pairs, n)
		if len(sel) == 0 {
			fmt.Fprintln(env.errw(), "no answers")
			return 1
		}
		var parts []string
		for _, p := range sel {
			parts = append(parts, "### Prompt\n"+p.Prompt+"\n\n### Answer\n"+p.Answer)
		}
		return doCopy(ctx, env, strings.Join(parts, "\n\n---\n\n"), "all")
	case "code":
		if len(pairs) == 0 {
			fmt.Fprintln(env.errw(), "no answers")
			return 1
		}
		blocks := fenceBlocks(pairs[len(pairs)-1].Answer)
		if len(blocks) == 0 {
			fmt.Fprintln(env.errw(), "no code blocks")
			return 1
		}
		n := len(blocks)
		if len(rest) > 0 {
			var errMsg string
			n, errMsg = parseN(rest, n, 5)
			if errMsg != "" {
				fmt.Fprintln(env.errw(), errMsg)
				return 2
			}
		}
		if n > 5 {
			n = 5
		}
		sel := lastN(blocks, n)
		return doCopy(ctx, env, strings.Join(sel, "\n\n"), "code")
	case "cmd":
		cmd := lastRunShell(ctx, env)
		if cmd == "" && len(pairs) > 0 {
			sh := fenceBlocks(pairs[len(pairs)-1].Answer, "bash", "sh", "shell", "zsh")
			if len(sh) > 0 {
				cmd = sh[len(sh)-1]
			}
		}
		if cmd == "" {
			fmt.Fprintln(env.errw(), "no command")
			return 1
		}
		return doCopy(ctx, env, cmd, "cmd")
	default:
		fmt.Fprintln(env.errw(), "usage: /copy answer|all|code|cmd [n]")
		return 2
	}
}

func doCopy(ctx context.Context, env *Env, text, what string) int {
	backend, err := env.copy(ctx, text)
	if err != nil {
		fmt.Fprintln(env.errw(), err)
		return 1
	}
	fmt.Fprintf(env.out(), "copied %s (%s) via %s\n", what, formatSize(len(text)), backend)
	return 0
}
