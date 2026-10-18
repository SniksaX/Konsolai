package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

func runRemember(ctx context.Context, env *Env, args []string) int {
	if env.Remember != nil {
		return env.Remember(ctx, args)
	}
	scope := "user"
	if len(args) > 0 && args[0] == "--project" {
		scope = "project"
		args = args[1:]
	}
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(env.errw(), "usage: /remember [--project] \"<fact>\"")
		return 2
	}
	s := env.Mem.Store()
	if s == nil {
		fmt.Fprintln(env.errw(), "memory is not configured (see ~/.config/konsolai/config.yaml)")
		return 1
	}
	fact := args[0]
	var err error
	if scope == "project" {
		pid := projectIDOf(ctx, env)
		if pid == nil {
			fmt.Fprintln(env.errw(), "konsolai: no git project here")
			return 1
		}
		_, err = s.AddProjectFact(ctx, *pid, fact, nil)
	} else {
		_, err = s.AddUserFact(ctx, env.Profile.Name, fact, "explicit")
	}
	if err != nil {
		fmt.Fprintln(env.errw(), "konsolai:", err)
		return 1
	}
	fmt.Fprintln(env.out(), "remembered")
	return 0
}

func runFacts(ctx context.Context, env *Env, _ []string) int {
	if env.Facts != nil {
		return env.Facts(ctx)
	}
	s := env.Mem.Store()
	if s == nil {
		fmt.Fprintln(env.errw(), "memory is not configured (see ~/.config/konsolai/config.yaml)")
		return 1
	}
	user, proj, err := s.ListFacts(ctx, env.Profile.Name, projectIDOf(ctx, env))
	if err != nil {
		fmt.Fprintln(env.errw(), "konsolai:", err)
		return 1
	}
	if len(user) == 0 && len(proj) == 0 {
		fmt.Fprintln(env.out(), "no facts")
		return 0
	}
	if len(user) > 0 {
		fmt.Fprintln(env.out(), "User facts:")
		for _, f := range user {
			fmt.Fprintf(env.out(), "  [%d] %s\n", f.ID, f.Fact)
		}
	}
	if len(proj) > 0 {
		fmt.Fprintln(env.out(), "Project facts:")
		for _, f := range proj {
			fmt.Fprintf(env.out(), "  [%d] %s\n", f.ID, f.Fact)
		}
	}
	return 0
}

func runForget(ctx context.Context, env *Env, args []string) int {
	if env.Forget != nil {
		return env.Forget(ctx, args)
	}
	if len(args) != 2 || (args[0] != "user" && args[0] != "project") {
		fmt.Fprintln(env.errw(), "usage: /forget user|project <id>")
		return 2
	}
	id, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || id < 1 {
		fmt.Fprintln(env.errw(), "usage: /forget user|project <id>")
		return 2
	}
	s := env.Mem.Store()
	if s == nil {
		fmt.Fprintln(env.errw(), "memory is not configured (see ~/.config/konsolai/config.yaml)")
		return 1
	}
	if err := s.DeleteFact(ctx, args[0], id); err != nil {
		fmt.Fprintln(env.errw(), "konsolai:", err)
		return 1
	}
	fmt.Fprintln(env.out(), "forgotten")
	return 0
}

func runPrune(ctx context.Context, env *Env, _ []string) int {
	if env.Prune != nil {
		return env.Prune(ctx)
	}
	s := env.Mem.Store()
	if s == nil {
		fmt.Fprintln(env.errw(), "memory is not configured (see ~/.config/konsolai/config.yaml)")
		return 1
	}
	days := env.Profile.Memory.RetentionDays
	if days < 1 {
		days = 30
	}
	if err := s.PruneIfDue(ctx, days); err != nil {
		fmt.Fprintln(env.errw(), "konsolai:", err)
		return 1
	}
	fmt.Fprintln(env.out(), "pruned")
	return 0
}

func runUndo(ctx context.Context, env *Env, _ []string) int {
	if env.Undo != nil {
		return env.Undo(ctx)
	}
	fmt.Fprintln(env.errw(), "undo is unavailable")
	return 1
}
