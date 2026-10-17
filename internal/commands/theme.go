package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/siraj/konsolai/internal/config"
	"github.com/siraj/konsolai/internal/ui"
	"github.com/siraj/konsolai/internal/ui/theme"
)

func runTheme(ctx context.Context, env *Env, args []string) int {
	if len(args) == 0 {
		printThemeList(env)
		return 0
	}
	if len(args) != 1 {
		fmt.Fprintln(env.errw(), "usage: /theme [name]")
		return 2
	}
	name, ok := theme.KnownPalette(args[0])
	if !ok {
		fmt.Fprintf(env.errw(), "unknown palette %q; use /theme\n", args[0])
		return 2
	}
	if env != nil {
		env.Profile.UI.Palette = name
		ui.ApplyUIOpts(name, env.Profile.Appearance(), env.Plain, env.Profile.CodeHighlightOn(), env.Profile.StripEmojiOn())
	}
	path := config.DefaultPath()
	if env != nil && env.ConfigPath != "" {
		path = env.ConfigPath
	}
	if err := config.SetPalette(path, name); err != nil {
		fmt.Fprintln(env.errw(), "konsolai:", err)
		return 1
	}
	fmt.Fprintln(env.out(), "palette "+name)
	return 0
}

func printThemeList(env *Env) {
	cur := ""
	if env != nil {
		cur = env.Profile.PaletteName()
	}
	appear := theme.AppearanceName()
	for _, name := range theme.PaletteNames {
		restore := theme.Force(true, theme.Mode() == theme.ColorTrue, name, appear)
		mark := "  "
		if name == cur {
			mark = "› "
		}
		line := mark + padName(name) + "  " +
			theme.Style(theme.Heading, "◆ act") + "  " +
			theme.Style(theme.Accent, "accent") + "  " +
			theme.Style(theme.Meta, "meta") + "  " +
			theme.Style(theme.Muted, "muted")
		restore()
		fmt.Fprintln(env.out(), line)
	}
}

func padName(name string) string {
	if len(name) >= 8 {
		return name
	}
	return name + strings.Repeat(" ", 8-len(name))
}
