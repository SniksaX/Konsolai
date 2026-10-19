package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/SniksaX/Konsolai/internal/turn"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
)

func runCmd(ctx context.Context, env *Env, args []string) int {
	if len(args) == 1 && strings.EqualFold(args[0], "clear") {
		if env.OnCmdClear != nil {
			env.OnCmdClear()
		}
		return 0
	}
	if len(args) != 0 {
		fmt.Fprintln(env.errw(), "usage: /cmd [clear]")
		return 2
	}
	if env.OnCmd != nil {
		return env.OnCmd()
	}
	fmt.Fprintln(env.errw(), "/cmd is only available in the REPL")
	return 2
}

func runExit(ctx context.Context, env *Env, _ []string) int {
	if env.OnExit != nil {
		return env.OnExit()
	}
	return 0
}

func runClear(ctx context.Context, env *Env, _ []string) int {
	fmt.Fprint(env.out(), theme.ClearScroll)
	if env.OnClear != nil {
		env.OnClear()
	}
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

func runThink(ctx context.Context, env *Env, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(env.errw(), "usage: /think on|off|auto")
		return 2
	}
	mode, ok := turn.ParseThinkMode(args[0])
	if !ok {
		fmt.Fprintln(env.errw(), "usage: /think on|off|auto")
		return 2
	}
	if env.Runner != nil {
		env.Runner.ThinkMode = mode
	}
	fmt.Fprintln(env.out(), "think "+strings.ToLower(args[0]))
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
	model := env.Profile.LLM.Model
	win := 0
	ep := env.Profile.LLM.Endpoint
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
