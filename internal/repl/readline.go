package repl

import (
	"context"
	"errors"
	"io"

	"github.com/reeflective/readline"
	"github.com/reeflective/readline/inputrc"
)

type rlReader struct {
	sh     *readline.Shell
	prompt string
	footer func() string
}

func NewReadline(completer func(line []rune, cursor int) readline.Completions, footer func() string) (LineReader, error) {
	sh := readline.NewShell()
	_ = sh.Config.Set("enable-bracketed-paste", true)
	_ = sh.Config.Set("history-size", histCap)
	_ = sh.Config.Set("cursor-position-probe", false)
	_ = sh.Config.Set("autocomplete", true)
	_ = sh.Config.Set("usage-hint-always", true)
	_ = sh.Config.Set("completion-description-style", "\x1b[38;5;245m")
	_ = sh.Config.Set("prompt-transient", true)
	_ = sh.Config.Set("transient-prompt", true)
	sh.History.Add("repl", loadFileHistory(HistoryPath()))
	sh.Keymap.Register(map[string]func(){
		"insert-newline": func() {
			sh.Line().Insert(sh.Cursor().Pos(), '\n')
			sh.Cursor().Inc()
		},
	})
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\M-\C-m`), "insert-newline", false)
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\e\r`), "insert-newline", false)
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\e\n`), "insert-newline", false)
	if completer != nil {
		sh.Completer = completer
	}
	r := &rlReader{sh: sh, footer: footer}
	sh.Prompt.Primary(func() string { return r.prompt })
	sh.Prompt.Transient(func() string { return r.prompt })
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
