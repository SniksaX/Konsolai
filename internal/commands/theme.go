package commands

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/siraj/konsolai/internal/banner"
	"github.com/siraj/konsolai/internal/config"
	"github.com/siraj/konsolai/internal/ui"
	"github.com/siraj/konsolai/internal/ui/theme"
	"golang.org/x/term"
)

func runTheme(ctx context.Context, env *Env, args []string) int {
	if len(args) == 0 {
		if env != nil && env.InRepl && pickerInteractive() {
			return runThemePicker(env)
		}
		printThemeRows(env, envTheme(env), -1)
		return 0
	}
	if len(args) != 1 {
		fmt.Fprintln(env.errw(), "usage: /theme [name]")
		return 2
	}
	return applyTheme(env, args[0], true)
}

func applyTheme(env *Env, raw string, save bool) int {
	name, ok := theme.KnownPalette(raw)
	if !ok {
		fmt.Fprintf(env.errw(), "unknown theme %q; use /theme\n", raw)
		return 2
	}
	setLiveTheme(env, name)
	if !save {
		return 0
	}
	path := config.DefaultPath()
	if env != nil && env.ConfigPath != "" {
		path = env.ConfigPath
	}
	if err := config.SetTheme(path, name); err != nil {
		fmt.Fprintln(env.errw(), "konsolai:", err)
		return 1
	}
	if env != nil {
		env.Profile.UI.Theme = name
	}
	fmt.Fprintln(env.out(), "theme "+name)
	return 0
}

func setLiveTheme(env *Env, name string) {
	appear := "auto"
	plain := false
	code := true
	strip := true
	if env != nil {
		env.Profile.UI.Theme = name
		appear = env.Profile.Appearance()
		plain = env.Plain
		code = env.Profile.CodeHighlightOn()
		strip = env.Profile.StripEmojiOn()
	}
	ui.ApplyUIOpts(name, appear, plain, code, strip)
	if env != nil && env.Profile.CursorColorOn() && theme.Enabled() {
		theme.ApplyCursor(env.out())
	}
}

func envTheme(env *Env) string {
	if env == nil {
		return theme.PaletteName()
	}
	return env.Profile.PaletteName()
}

func pickerInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func runThemePicker(env *Env) int {
	names := theme.PaletteNames
	prev := envTheme(env)
	sel := indexOf(names, prev)
	if sel < 0 {
		sel = 0
	}
	preview := func(i int) {
		setLiveTheme(env, names[i])
		redrawPreview(env)
		printThemeRows(env, names[i], i)
	}
	preview(sel)
	for {
		k, err := readPickerKey()
		if err != nil {
			setLiveTheme(env, prev)
			fmt.Fprintln(env.errw(), "konsolai:", err)
			return 1
		}
		switch k {
		case "up":
			if sel > 0 {
				sel--
				preview(sel)
			}
		case "down":
			if sel < len(names)-1 {
				sel++
				preview(sel)
			}
		case "enter":
			return applyTheme(env, names[sel], true)
		case "esc":
			setLiveTheme(env, prev)
			redrawPreview(env)
			return 0
		}
	}
}

func redrawPreview(env *Env) {
	w := env.out()
	if f, ok := w.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(w, theme.ClearHome)
	}
	info := banner.Info{
		Version: env.Version,
		Model:   env.Profile.Model,
		Dir:     env.Cwd,
		Host:    banner.HostOf(env.Profile.Endpoint),
	}
	_ = banner.Render(w, info, ui.Current(), theme.TermWidth(), env.Profile.ShowBanner())
}

func printThemeRows(env *Env, current string, selected int) {
	w := env.out()
	appear := theme.AppearanceName()
	for i, name := range theme.PaletteNames {
		restore := theme.Force(true, theme.Mode() == theme.ColorTrue, name, appear)
		mark := "  "
		namePaint := theme.Style(theme.Muted, padName(name))
		if (selected >= 0 && i == selected) || (selected < 0 && name == current) {
			mark = "› "
			namePaint = theme.Style(theme.Heading, padName(name))
		}
		bar := gradientBar()
		dot := theme.Style(theme.Accent, "●")
		restore()
		fmt.Fprintln(w, mark+namePaint+"  "+bar+"  "+dot)
	}
}

func gradientBar() string {
	cells := theme.Gradient(10, theme.RoleRGB(theme.BannerTop), theme.RoleRGB(theme.BannerBottom))
	var b strings.Builder
	for _, c := range cells {
		b.WriteString(theme.PaintRGB(c, "▀"))
	}
	return b.String()
}

func padName(name string) string {
	if len(name) >= 8 {
		return name
	}
	return name + strings.Repeat(" ", 8-len(name))
}

func indexOf(names []string, want string) int {
	for i, n := range names {
		if n == want {
			return i
		}
	}
	return -1
}

func readPickerKey() (string, error) {
	b, err := ui.ReadKey()
	if err != nil {
		return "", err
	}
	switch b {
	case '\r', '\n':
		return "enter", nil
	case 0x03:
		return "esc", nil
	case 0x1b:
		return readArrowOrEsc()
	default:
		return "", nil
	}
}

func readArrowOrEsc() (string, error) {
	_ = os.Stdin.SetReadDeadline(time.Now().Add(40 * time.Millisecond))
	defer func() { _ = os.Stdin.SetReadDeadline(time.Time{}) }()
	b, err := ui.ReadKey()
	if err != nil {
		return "esc", nil
	}
	if b != '[' && b != 'O' {
		return "esc", nil
	}
	_ = os.Stdin.SetReadDeadline(time.Now().Add(40 * time.Millisecond))
	c, err := ui.ReadKey()
	if err != nil {
		return "esc", nil
	}
	switch c {
	case 'A':
		return "up", nil
	case 'B':
		return "down", nil
	default:
		return "esc", nil
	}
}
