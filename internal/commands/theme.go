package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/siraj/konsolai/internal/config"
	"github.com/siraj/konsolai/internal/ui"
	"github.com/siraj/konsolai/internal/ui/theme"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

var welcomeRedraw sync.Map

// SetWelcomeRedraw installs the REPL hook that paints the cached welcome
// after /theme Enter. A nil fn clears it.
func SetWelcomeRedraw(env *Env, fn func()) {
	if env == nil {
		return
	}
	if fn == nil {
		welcomeRedraw.Delete(env)
		return
	}
	welcomeRedraw.Store(env, fn)
}

func takeWelcomeRedraw(env *Env) func() {
	if env == nil {
		return nil
	}
	v, ok := welcomeRedraw.Load(env)
	if !ok {
		return nil
	}
	fn, _ := v.(func())
	return fn
}

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
	return applyTheme(env, args[0], true, true)
}

func applyTheme(env *Env, raw string, save, announce bool) int {
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
	if announce {
		fmt.Fprintln(env.out(), "theme "+name)
	}
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
	rows := 1 + len(names)
	painted := false
	paint := func() {
		w := env.out()
		if painted {
			fmt.Fprint(w, theme.CursorUp(rows))
		}
		painted = true
		fmt.Fprint(w, theme.ClearLine)
		fmt.Fprintln(w, themeSample())
		printThemeRows(env, names[sel], sel)
	}
	setLiveTheme(env, names[sel])
	paint()
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
				setLiveTheme(env, names[sel])
				paint()
			}
		case "down":
			if sel < len(names)-1 {
				sel++
				setLiveTheme(env, names[sel])
				paint()
			}
		case "enter":
			code := applyTheme(env, names[sel], true, false)
			if code != 0 {
				return code
			}
			fmt.Fprint(env.out(), theme.ClearScroll)
			if fn := takeWelcomeRedraw(env); fn != nil {
				fn()
			}
			fmt.Fprintln(env.out(), "theme "+names[sel])
			return 0
		case "esc":
			setLiveTheme(env, prev)
			if painted {
				fmt.Fprint(env.out(), theme.CursorUp(rows))
				fmt.Fprint(env.out(), theme.ClearBelow)
			}
			return 0
		}
	}
}

func themeSample() string {
	p := ui.Current()
	return "  " + p.Accent("◆") + " " + p.Heading("build") + "  " +
		p.Accent("▸") + " " + theme.Style(theme.Primary, "read") + " " + p.Meta("go.mod") + "  " +
		p.Success("✓") + " " + p.Muted("479 B · 0.0s")
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
		if selected >= 0 {
			fmt.Fprint(w, theme.ClearLine)
		}
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

func inputReady(wait time.Duration) bool {
	ms := int(wait.Milliseconds())
	if ms < 1 {
		ms = 1
	}
	pfd := []unix.PollFd{{
		Fd:     int32(os.Stdin.Fd()),
		Events: unix.POLLIN,
	}}
	for {
		n, err := unix.Poll(pfd, ms)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return err == nil && n > 0 && pfd[0].Revents&unix.POLLIN != 0
	}
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
	// PTYs reject SetReadDeadline, so a lone Esc is separated from an arrow
	// by polling the kernel buffer.
	if !inputReady(40 * time.Millisecond) {
		return "esc", nil
	}
	b, err := ui.ReadKey()
	if err != nil {
		return "esc", nil
	}
	if b != '[' && b != 'O' {
		return "esc", nil
	}
	if !inputReady(40 * time.Millisecond) {
		return "esc", nil
	}
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
