package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/siraj/konsolai/internal/turn"
)

func runExit(ctx context.Context, env *Env, _ []string) int {
	if env.OnExit != nil {
		return env.OnExit()
	}
	return 0
}

func runClear(ctx context.Context, env *Env, _ []string) int {
	fmt.Fprint(env.out(), "\033[H\033[2J")
	return 0
}

func runCD(ctx context.Context, env *Env, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(env.errw(), "usage: /cd <dir>")
		return 2
	}
	if env.OnCwd == nil {
		fmt.Fprintln(env.errw(), "/cd is only available in the REPL")
		return 2
	}
	if err := env.OnCwd(args[0]); err != nil {
		fmt.Fprintln(env.errw(), "konsolai:", err)
		return 1
	}
	fmt.Fprintln(env.out(), env.Cwd)
	return 0
}

func runYes(ctx context.Context, env *Env, args []string) int {
	on, ok := parseOnOff(args)
	if !ok {
		fmt.Fprintln(env.errw(), "usage: /yes on|off")
		return 2
	}
	if env.OnYes != nil {
		env.OnYes(on)
	} else {
		env.Yes = on
	}
	if on {
		fmt.Fprintln(env.out(), "auto-approve on")
	} else {
		fmt.Fprintln(env.out(), "auto-approve off")
	}
	return 0
}

func runTrace(ctx context.Context, env *Env, args []string) int {
	on, ok := parseOnOff(args)
	if !ok {
		fmt.Fprintln(env.errw(), "usage: /trace on|off")
		return 2
	}
	if env.OnTrace != nil {
		env.OnTrace(on)
	}
	if on {
		fmt.Fprintln(env.out(), "trace on")
	} else {
		fmt.Fprintln(env.out(), "trace off")
	}
	return 0
}

func runModel(ctx context.Context, env *Env, _ []string) int {
	model := env.Profile.Model
	win := 0
	ep := env.Profile.Endpoint
	if env.Runner != nil && env.Runner.Client != nil {
		if id, err := env.Runner.Client.ResolveModel(ctx); err == nil && id != "" {
			model = id
		}
		win = env.Runner.Client.ContextWindow()
	}
	if model == "" {
		model = "-"
	}
	fmt.Fprintf(env.out(), "model    %s\n", model)
	if win > 0 {
		fmt.Fprintf(env.out(), "context  %d\n", win)
	}
	fmt.Fprintf(env.out(), "endpoint %s\n", turn.EndpointHost(ep))
	return 0
}

func runCompact(ctx context.Context, env *Env, _ []string) int {
	if env.OnCompact != nil {
		return env.OnCompact(ctx)
	}
	fmt.Fprintln(env.errw(), "/compact is only available in the REPL")
	return 2
}

func parseOnOff(args []string) (bool, bool) {
	if len(args) != 1 {
		return false, false
	}
	switch strings.ToLower(args[0]) {
	case "on", "true", "1":
		return true, true
	case "off", "false", "0":
		return false, true
	default:
		return false, false
	}
}
