package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/siraj/konsolai/internal/config"
)

func runConfig(ctx context.Context, env *Env, args []string) int {
	path := config.DefaultPath()
	if env != nil && env.ConfigPath != "" {
		path = env.ConfigPath
	}
	sub := "path"
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	switch sub {
	case "path":
		fmt.Fprintln(env.out(), path)
		return 0
	case "show":
		if env == nil {
			return 1
		}
		text, err := env.Profile.ShowYAML()
		if err != nil {
			fmt.Fprintln(env.errw(), "konsolai:", err)
			return 1
		}
		fmt.Fprint(env.out(), text)
		return 0
	case "init":
		force := false
		for _, a := range args[1:] {
			if a == "--force" {
				force = true
			}
		}
		if err := config.WriteInit(path, force); err != nil {
			fmt.Fprintln(env.errw(), "konsolai:", err)
			return 1
		}
		fmt.Fprintln(env.out(), path)
		return 0
	case "validate":
		f, err := config.Load(path)
		if err != nil {
			fmt.Fprintln(env.errw(), err)
			return 1
		}
		name := ""
		if env != nil {
			name = env.Profile.Name
		}
		if _, err := f.Resolve(name); err != nil {
			fmt.Fprintln(env.errw(), err)
			return 1
		}
		fmt.Fprintln(env.out(), "ok")
		return 0
	default:
		fmt.Fprintln(env.errw(), "usage: /config path|show|init [--force]|validate")
		return 2
	}
}
