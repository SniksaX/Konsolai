package commands

import (
	"context"
	"fmt"
	"strings"
)

func runOutput(ctx context.Context, env *Env, args []string) int {
	if len(args) > 1 {
		fmt.Fprintln(env.errw(), "usage: /output [n]")
		return 2
	}
	n := 1
	if len(args) == 1 {
		var msg string
		n, msg = parseN(args, 1, 5)
		if msg != "" {
			fmt.Fprintln(env.errw(), "usage: /output [n]")
			return 2
		}
	}
	idx := n - 1
	if env.Runner != nil {
		if s, ok := env.Runner.ShellOutput(idx); ok {
			printOutput(env, s)
			return 0
		}
	}
	if env.Mem != nil && env.Mem.Store() != nil && env.Profile.Name != "" {
		outs, err := env.Mem.Store().RecentShellOutputs(ctx, env.Profile.Name, idx+1)
		if err == nil && idx < len(outs) {
			printOutput(env, outs[idx])
			return 0
		}
	}
	fmt.Fprintln(env.errw(), "no shell output")
	return 1
}

func printOutput(env *Env, s string) {
	fmt.Fprint(env.out(), s)
	if !strings.HasSuffix(s, "\n") {
		fmt.Fprintln(env.out())
	}
}
