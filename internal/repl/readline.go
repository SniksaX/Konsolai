package repl

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/reeflective/readline"
	"github.com/reeflective/readline/inputrc"
)

type rlReader struct {
	sh       *readline.Shell
	prompt   string
	footer   func() string
	pastes   *PasteStore
	lastChip string
	shellOn  bool
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
	_ = sh.Config.Set("completion-description-style", "\x1b[38;5;245m")
	// Leave the live prompt as the only scrollback copy. RefreshTransient
	// reprints prompt+line after accept and misses the footer/hint row, so
	// enabling it produced a second "› line" in a real TTY.
	_ = sh.Config.Set("prompt-transient", false)
	_ = sh.Config.Set("transient-prompt", false)
	r := &rlReader{sh: sh, footer: footer, pastes: pastes}
	sh.History.Add("repl", &gatedHistory{inner: loadFileHistory(HistoryPath()), active: &r.shellOn, want: false})
	sh.History.Add("shell", &gatedHistory{inner: loadFileHistory(ShellHistoryPath()), active: &r.shellOn, want: true})
	sh.Keymap.Register(map[string]func(){
		"insert-newline": func() {
			sh.Line().Insert(sh.Cursor().Pos(), '\n')
			sh.Cursor().Inc()
		},
		"undo-or-expand-paste": r.undoOrExpand,
	})
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\M-\C-m`), "insert-newline", false)
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\e\r`), "insert-newline", false)
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\e\n`), "insert-newline", false)
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\C-z`), "undo-or-expand-paste", false)
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
		if r.footer == nil {
			return nil
		}
		s := r.footer()
		if s == "" {
			return nil
		}
		return []rune(s)
	})
	return r, nil
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

func (r *rlReader) ReadLine(_ context.Context, prompt string) (string, error) {
	r.prompt = prompt
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
