package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/SniksaX/Konsolai/internal/config"
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
		real, err := config.RealPath(path)
		if err != nil {
			fmt.Fprintln(env.errw(), err)
			return 1
		}
		fmt.Fprintln(env.out(), path)
		fmt.Fprintln(env.out(), real)
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
		fromCurrent := false
		for _, a := range args[1:] {
			switch a {
			case "--force":
				force = true
			case "--from-current":
				fromCurrent = true
			default:
				fmt.Fprintln(env.errw(), "usage: /config path|show|validate|diff|init [--force|--from-current]")
				return 2
			}
		}
		var err error
		if fromCurrent {
			err = config.WriteCurrent(path, force)
		} else {
			err = config.WriteInit(path, force)
		}
		if err != nil {
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
	case "diff":
		name := ""
		if env != nil {
			name = env.Profile.Name
		}
		text, err := config.DiffText(path, name)
		if err != nil {
			fmt.Fprintln(env.errw(), err)
			return 1
		}
		fmt.Fprint(env.out(), text)
		return 0
	default:
		fmt.Fprintln(env.errw(), "usage: /config path|show|validate|diff|init [--force|--from-current]")
		return 2
	}
}
