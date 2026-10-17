package repl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/reeflective/readline"
	"github.com/reeflective/readline/inputrc"
	"github.com/siraj/konsolai/internal/ui"
	"github.com/siraj/konsolai/internal/ui/theme"
	"golang.org/x/term"
)

type rlReader struct {
	sh          *readline.Shell
	prompt      string
	innerPrompt string
	footer      func() string
	onClear     func()
	pastes      *PasteStore
	lastChip    string
	shellOn     bool
	framed      bool
}

type gatedHistory struct {
	inner  *fileHistory
	active *bool
	want   bool
}

func (g *gatedHistory) Write(line string) (int, error) {
	if g.active == nil || *g.active != g.want {
		return g.inner.Len(), nil
	}
	return g.inner.Write(line)
}

func (g *gatedHistory) GetLine(pos int) (string, error) { return g.inner.GetLine(pos) }
func (g *gatedHistory) Len() int                        { return g.inner.Len() }
func (g *gatedHistory) Dump() interface{}               { return g.inner.Dump() }

func NewReadline(completer func(line []rune, cursor int) readline.Completions, footer func() string, pastes *PasteStore) (LineReader, error) {
	sh := readline.NewShell()
	_ = sh.Config.Set("enable-bracketed-paste", true)
	_ = sh.Config.Set("history-size", histCap)
	_ = sh.Config.Set("cursor-position-probe", false)
	_ = sh.Config.Set("autocomplete", true)
	_ = sh.Config.Set("usage-hint-always", true)
	_ = sh.Config.Set("completion-description-style", ui.Current().MutedSeq())
	// Leave the live prompt as the only scrollback copy. RefreshTransient
	// reprints prompt+line after accept and misses the footer/hint row, so
	// enabling it produced a second "› line" in a real TTY.
	_ = sh.Config.Set("prompt-transient", false)
	_ = sh.Config.Set("transient-prompt", false)
	r := &rlReader{sh: sh, footer: footer, pastes: pastes}
	sh.History.Add("repl", &gatedHistory{inner: loadFileHistory(HistoryPath()), active: &r.shellOn, want: false})
	sh.History.Add("shell", &gatedHistory{inner: loadFileHistory(ShellHistoryPath()), active: &r.shellOn, want: true})
	r.framed = term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stdin.Fd()))
	cmds := sh.Keymap.Commands()
	sh.Keymap.Register(map[string]func(){
		"insert-newline": func() {
			sh.Line().Insert(sh.Cursor().Pos(), '\n')
			sh.Cursor().Inc()
		},
		"undo-or-expand-paste": r.undoOrExpand,
		"konsolai-clear":       r.clearScreen,
		"konsolai-up":          r.historyOrMenu(true, cmds),
		"konsolai-down":        r.historyOrMenu(false, cmds),
	})
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\M-\C-m`), "insert-newline", false)
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\e\r`), "insert-newline", false)
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\e\n`), "insert-newline", false)
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\C-z`), "undo-or-expand-paste", false)
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\C-l`), "konsolai-clear", false)
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\e[A`), "konsolai-up", false)
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\e[B`), "konsolai-down", false)
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\eOA`), "konsolai-up", false)
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\eOB`), "konsolai-down", false)
	if completer != nil {
		sh.Completer = completer
	}
	sh.PasteTransformer = func(text string) string {
		if r.pastes == nil {
			r.lastChip = ""
			return text
		}
		chip, ok := r.pastes.MaybeChip(text)
		if !ok {
			r.lastChip = ""
			return text
		}
		r.lastChip = chip
		return chip
	}
	sh.Prompt.Primary(func() string { return r.prompt })
	sh.Hint.SetProvider(func(line []rune, cursor int) []rune {
		if !r.framed {
			if r.footer == nil {
				return nil
			}
			s := r.footer()
			if s == "" {
				return nil
			}
			return []rune(s)
		}
		width := theme.TermWidth()
		rule := frameRule(width)
		foot := ""
		if r.footer != nil {
			foot = r.footer()
		}
		if foot == "" {
			return []rune(rule)
		}
		return []rune(rule + "\n" + foot)
	})
	return r, nil
}

func frameRule(width int) string {
	if width < 1 {
		width = 80
	}
	return theme.Style(theme.Subtle, strings.Repeat("─", width))
}

func (r *rlReader) undoOrExpand() {
	if r.sh == nil {
		return
	}
	line := string(*r.sh.Line())
	if r.lastChip != "" && strings.Contains(line, r.lastChip) {
		raw := r.lastChip
		if r.pastes != nil {
			if _, body, ok := r.pastes.LastChip(); ok {
				raw = body
			}
		}
		r.sh.History.Undo()
		r.sh.History.Reset()
		r.sh.Cursor().InsertAt([]rune(raw)...)
		r.lastChip = ""
		return
	}
	r.sh.History.Undo()
}

func (r *rlReader) historyOrMenu(up bool, cmds map[string]func()) func() {
	return func() {
		line := ""
		if r.sh != nil && r.sh.Line() != nil {
			line = string(*r.sh.Line())
		}
		if strings.HasPrefix(line, "/") {
			name := "menu-complete"
			if up {
				name = "menu-complete-backward"
			}
			if fn := cmds[name]; fn != nil {
				fn()
				return
			}
		}
		name := "next-history"
		if up {
			name = "previous-history"
		}
		if fn := cmds[name]; fn != nil {
			fn()
		}
	}
}

func (r *rlReader) SetOnClear(fn func()) { r.onClear = fn }

func (r *rlReader) clearScreen() {
	fmt.Fprint(os.Stdout, theme.ClearHome)
	if r.onClear != nil {
		r.onClear()
	}
}

func (r *rlReader) EraseFrame(w io.Writer, line string) {
	if r == nil || !r.framed {
		return
	}
	// Hints, menu and footer are already cleared by readline at accept.
	// Only the top rule (printed by us) and the wrapped input remain.
	width := theme.TermWidth()
	inLines := theme.VisualLines(r.innerPrompt+line, width)
	up := 1 + inLines
	fmt.Fprint(w, theme.CursorUp(up)+theme.ClearBelow)
}

func (r *rlReader) printTopRule() {
	if r == nil || !r.framed {
		return
	}
	fmt.Fprintln(os.Stdout, frameRule(theme.TermWidth()))
}

func (r *rlReader) ReadLine(_ context.Context, prompt string) (string, error) {
	r.innerPrompt = prompt
	r.prompt = prompt
	r.printTopRule()
	line, err := r.sh.Readline()
	if err != nil {
		if errors.Is(err, readline.ErrInterrupt) {
			return line, ErrInterrupt
		}
		if errors.Is(err, io.EOF) {
			return line, ErrEOF
		}
		return line, err
	}
	return line, nil
}

func (r *rlReader) Close() error { return nil }

func (r *rlReader) SetShellMode(on bool) {
	if r == nil || r.sh == nil || r.shellOn == on {
		return
	}
	if on {
		r.sh.History.Cycle(true)
	} else {
		r.sh.History.Cycle(false)
	}
	r.shellOn = on
}
