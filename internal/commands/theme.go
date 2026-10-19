package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/SniksaX/Konsolai/internal/ui"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func runTheme(ctx context.Context, env *Env, args []string) int {
	fmt.Fprintln(env.out(), ui.Current().Muted("now: /set theme"))
	return runSet(ctx, env, append([]string{"theme"}, args...))
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
			if painted {
				fmt.Fprint(env.out(), theme.CursorUp(rows))
				fmt.Fprint(env.out(), theme.ClearBelow)
			}
			return setOne(env, "ui.theme", []string{names[sel]}, false)
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
