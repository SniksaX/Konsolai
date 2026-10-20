package repl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/ui"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
	"github.com/reeflective/readline"
	"github.com/reeflective/readline/inputrc"
	"golang.org/x/term"
)

type rlReader struct {
	sh          *readline.Shell
	prompt      string
	innerPrompt string
	footer      func() string
	onClear     func()
	pastes      *PasteStore
	menu        *slashMenu
	at          *atMenu
	escTimeout  func() time.Duration
	lastChip    string
	shellOn     bool
	framed      bool
	placeholder string
	phMu        sync.Mutex
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

func NewReadline(completer func(line []rune, cursor int) readline.Completions, footer func() string, pastes *PasteStore, reg *commands.Registry) (LineReader, error) {
	sh := readline.NewShell()
	_ = sh.Config.Set("enable-bracketed-paste", true)
	_ = sh.Config.Set("history-size", histCap)
	_ = sh.Config.Set("cursor-position-probe", false)
	// Autocomplete would steal arrows via menuselect. Shell-mode still
	// completes on Tab. @ is the file menu, not readline candidates.
	_ = sh.Config.Set("autocomplete", false)
	_ = sh.Config.Set("keyseq-timeout", 500)
	_ = sh.Config.Set("usage-hint-always", true)
	_ = sh.Config.Set("completion-description-style", ui.Current().MutedSeq())
	_ = sh.Config.Set("completion-selection-style", theme.HeadingSeq())
	// Leave the live prompt as the only scrollback copy. RefreshTransient
	// reprints prompt+line after accept and misses the footer/hint row, so
	// enabling it produced a second "› line" in a real TTY.
	_ = sh.Config.Set("prompt-transient", false)
	_ = sh.Config.Set("transient-prompt", false)
	r := &rlReader{sh: sh, footer: footer, pastes: pastes, menu: newSlashMenu(reg), at: newAtMenu(func() string {
		wd, err := os.Getwd()
		if err != nil {
			return ""
		}
		return wd
	})}
	sh.History.Add("repl", &gatedHistory{inner: loadFileHistory(HistoryPath()), active: &r.shellOn, want: false})
	sh.History.Add("shell", &gatedHistory{inner: loadFileHistory(ShellHistoryPath()), active: &r.shellOn, want: true})
	r.framed = term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stdin.Fd()))
	sh.Keymap.Register(map[string]func(){
		"insert-newline": func() {
			sh.Line().Insert(sh.Cursor().Pos(), '\n')
			sh.Cursor().Inc()
		},
		"undo-or-expand-paste": r.undoOrExpand,
		"konsolai-clear":       r.clearScreen,
		"konsolai-up":          r.slashUp,
		// Stock forward-char and vi-forward-char call acceptInlineSuggestion,
		// which would submit the placeholder. End-of-line does not accept in
		// v1.3.0; the override keeps that and stays off the suggestion.
		"forward-char":      r.forwardNoAccept,
		"vi-forward-char":   r.viForwardNoAccept,
		"end-of-line":       r.endNoAccept,
		"konsolai-down":     r.slashDown,
		"konsolai-esc":      r.slashEsc,
		"konsolai-tab":      r.slashTab,
		"konsolai-enter":    r.slashEnter,
		"konsolai-question": r.slashQuestion,
	})
	bindEmacsVi := func(seq, action string) {
		key := inputrc.Unescape(seq)
		for _, km := range []string{"emacs", "emacs-standard", "emacs-meta", "emacs-ctlx", "vi-insert"} {
			_ = sh.Config.Bind(km, key, action, false)
		}
	}
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\M-\C-m`), "insert-newline", false)
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\e\r`), "insert-newline", false)
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\e\n`), "insert-newline", false)
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\C-z`), "undo-or-expand-paste", false)
	_ = sh.Config.Bind("emacs", inputrc.Unescape(`\C-l`), "konsolai-clear", false)
	// Default emacs binds store arrows as \M-[A (a meta rune). ConvertMeta
	// maps those onto the same bytes as \e[A, and the meta entry sorts last,
	// so binding only \e[A left previous-history/next-history in charge.
	bindEmacsVi(`\e[A`, "konsolai-up")
	bindEmacsVi(`\M-[A`, "konsolai-up")
	bindEmacsVi(`\e[B`, "konsolai-down")
	bindEmacsVi(`\M-[B`, "konsolai-down")
	bindEmacsVi(`\eOA`, "konsolai-up")
	bindEmacsVi(`\M-OA`, "konsolai-up")
	bindEmacsVi(`\eOB`, "konsolai-down")
	bindEmacsVi(`\M-OB`, "konsolai-down")
	// Home/End/Delete forms readline does not bind. The escape reader
	// dispatches the emacs action for the whole sequence.
	bindEmacsVi(`\e[3~`, "delete-char")
	bindEmacsVi(`\M-[3~`, "delete-char")
	bindEmacsVi(`\e[1~`, "beginning-of-line")
	bindEmacsVi(`\M-[1~`, "beginning-of-line")
	bindEmacsVi(`\e[4~`, "end-of-line")
	bindEmacsVi(`\M-[4~`, "end-of-line")
	bindEmacsVi(`\e[7~`, "beginning-of-line")
	bindEmacsVi(`\M-[7~`, "beginning-of-line")
	bindEmacsVi(`\e[8~`, "end-of-line")
	bindEmacsVi(`\M-[8~`, "end-of-line")
	bindEmacsVi(`\e`, "konsolai-esc")
	bindEmacsVi(`\C-i`, "konsolai-tab")
	bindEmacsVi(`\C-m`, "konsolai-enter")
	bindEmacsVi(`\C-j`, "konsolai-enter")
	bindEmacsVi(`?`, "konsolai-question")
	// Reeflective has no inter-byte timeout, so a longer Esc bind would
	// make a lone Esc wait forever. This local map matches Esc at once.
	// slashEsc then reads the rest of the sequence (CSI, SS3, or Alt+key)
	// and dispatches that bind. Only a lone Esc reaches the menu.
	if sh.Config.Binds["slash-esc"] == nil {
		sh.Config.Binds["slash-esc"] = map[string]inputrc.Bind{}
	}
	sh.Config.Binds["slash-esc"][inputrc.Unescape(`\e`)] = inputrc.Bind{Action: "konsolai-esc", Macro: false}
	sh.Keymap.SetLocal("slash-esc")
	sh.Completer = func(line []rune, cursor int) readline.Completions {
		s := string(line)
		if cursor >= 0 && cursor <= len(line) {
			s = string(line[:cursor])
		}
		if strings.HasPrefix(s, "/") && !strings.Contains(s, " ") {
			return readline.Completions{}
		}
		if completer != nil {
			return completer(line, cursor)
		}
		return readline.Completions{}
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
	sh.SyntaxHighlighter = func(line []rune) string {
		var reg *commands.Registry
		if r.menu != nil {
			reg = r.menu.reg
		}
		return highlightInput(string(line), reg)
	}
	sh.Hint.SetProvider(func(line []rune, cursor int) []rune {
		r.syncPlaceholder(line)
		s := string(line)
		if r.menu != nil {
			r.menu.Sync(s)
		}
		if r.at != nil {
			r.at.Sync(s, cursor)
		}
		r.gateAutocomplete(s)
		r.ensureEscLocal()
		width := theme.TermWidth()
		menu := ""
		if r.at != nil && r.at.Open() {
			menu = r.at.Render(width)
		} else if r.menu != nil && r.menu.Open() {
			menu = r.menu.Render(width)
		}
		foot := r.hintFooter()
		if !r.framed {
			switch {
			case menu != "" && foot != "":
				return []rune(menu + "\n" + foot)
			case menu != "":
				return []rune(menu)
			case foot != "":
				return []rune(foot)
			default:
				return nil
			}
		}
		rule := frameRule(width)
		var b strings.Builder
		if menu != "" {
			b.WriteString(menu)
			b.WriteByte('\n')
		}
		b.WriteString(rule)
		if foot != "" {
			b.WriteByte('\n')
			b.WriteString(foot)
		}
		return []rune(b.String())
	})
	return r, nil
}

func (r *rlReader) SetPlaceholder(s string) {
	if strings.TrimSpace(s) == "" {
		s = "ask anything"
	}
	r.phMu.Lock()
	r.placeholder = s
	r.phMu.Unlock()
}

func (r *rlReader) currentPlaceholder() string {
	if r == nil {
		return ""
	}
	r.phMu.Lock()
	defer r.phMu.Unlock()
	return r.placeholder
}

func (r *rlReader) syncPlaceholder(line []rune) {
	if r == nil || r.sh == nil {
		return
	}
	ph := r.currentPlaceholder()
	hide := r.shellOn || len(line) != 0 || ph == "" || (r.menu != nil && r.menu.Open()) || (r.at != nil && r.at.Open())
	if hide {
		if r.sh.GetInlineSuggestion() != "" {
			r.sh.ClearInlineSuggestion()
		}
		return
	}
	if r.sh.GetInlineSuggestion() != ph {
		r.sh.SetInlineSuggestion(ph)
	}
}

func (r *rlReader) forwardNoAccept() {
	if r == nil || r.sh == nil || r.sh.Cursor() == nil {
		return
	}
	r.sh.History.SkipSave()
	vii := 1
	if r.sh.Iterations != nil {
		vii = r.sh.Iterations.Get()
	}
	for i := 1; i <= vii; i++ {
		r.sh.Cursor().Inc()
	}
}

func (r *rlReader) endNoAccept() {
	if r == nil || r.sh == nil || r.sh.Cursor() == nil {
		return
	}
	r.sh.History.SkipSave()
	r.sh.Cursor().EndOfLineAppend()
}

func (r *rlReader) viForwardNoAccept() {
	if r == nil || r.sh == nil || r.sh.Cursor() == nil || r.sh.Line() == nil {
		return
	}
	name := ""
	if r.sh.Keymap != nil {
		name = fmt.Sprint(r.sh.Keymap.Main())
	}
	if name == "vi-insert" {
		return
	}
	r.sh.History.SkipSave()
	line := r.sh.Line()
	cur := r.sh.Cursor()
	if cur.Pos() >= line.Len()-1 {
		return
	}
	vii := 1
	if r.sh.Iterations != nil {
		vii = r.sh.Iterations.Get()
	}
	for i := 1; i <= vii; i++ {
		if cur.Pos()+1 < line.Len() && (*line)[cur.Pos()+1] == '\n' {
			break
		}
		cur.Inc()
	}
}

func (r *rlReader) ensureEscLocal() {
	if r == nil || r.sh == nil || r.sh.Keymap == nil {
		return
	}
	if r.sh.Keymap.Local() == "" {
		r.sh.Keymap.SetLocal("slash-esc")
	}
}

func (r *rlReader) hintFooter() string {
	if r.at != nil && r.at.Open() {
		return r.at.HintFooter()
	}
	if r.menu != nil && r.menu.Open() {
		return r.menu.HintFooter()
	}
	if r.footer != nil {
		return r.footer()
	}
	return ""
}

func (r *rlReader) gateAutocomplete(line string) {
	if r == nil || r.sh == nil {
		return
	}
	// As-you-type complete steals arrows via menu-select. Tab still works.
	_ = r.sh.Config.Set("autocomplete", false)
	_ = line
}

func (r *rlReader) syncMenu() {
	if r == nil || r.sh == nil || r.sh.Line() == nil {
		return
	}
	line := string(*r.sh.Line())
	cur := 0
	if r.sh.Cursor() != nil {
		cur = r.sh.Cursor().Pos()
	}
	if r.menu != nil {
		r.menu.Sync(line)
	}
	if r.at != nil {
		r.at.Sync(line, cur)
	}
	r.gateAutocomplete(line)
	r.ensureEscLocal()
}

func (r *rlReader) applyLine(s string) {
	if r == nil || r.sh == nil || r.sh.Line() == nil {
		return
	}
	r.sh.History.Save()
	rs := []rune(s)
	r.sh.Line().Set(rs...)
	r.sh.Cursor().Set(len(rs))
}

func (r *rlReader) callWidget(name string) {
	if r == nil || r.sh == nil {
		return
	}
	if fn := r.sh.Keymap.Commands()[name]; fn != nil {
		fn()
	}
}

func (r *rlReader) applyMenu(res menuResult) {
	switch res.Action {
	case actionLine:
		r.applyLine(res.Line)
		if res.KeepCursor && r.sh.Cursor() != nil {
			r.sh.Cursor().Set(res.Cursor)
		}
	case actionSubmit:
		r.callWidget("accept-line")
	case actionLineSubmit:
		r.applyLine(res.Line)
		r.callWidget("accept-line")
	case actionClear:
		r.applyLine("")
	}
}

func (r *rlReader) SetMenuEnv(env *commands.Env) {
	if r != nil && r.menu != nil {
		r.menu.env = env
	}
}

func (r *rlReader) slashDown() {
	r.syncMenu()
	if r.at != nil && r.at.Open() {
		r.applyMenu(r.at.Down())
		return
	}
	if r.menu != nil && r.menu.Open() {
		r.applyMenu(r.menu.Down())
		return
	}
	r.callWidget("next-history")
}

func (r *rlReader) slashUp() {
	r.syncMenu()
	if r.at != nil && r.at.Open() {
		r.applyMenu(r.at.Up())
		return
	}
	if r.menu != nil && r.menu.Open() {
		r.applyMenu(r.menu.Up())
		return
	}
	r.callWidget("previous-history")
}

func (r *rlReader) SetEscTimeout(fn func() time.Duration) {
	if r == nil {
		return
	}
	r.escTimeout = fn
}

func (r *rlReader) escWait() time.Duration {
	if r != nil && r.escTimeout != nil {
		if d := r.escTimeout(); d > 0 {
			return d
		}
	}
	return 30 * time.Millisecond
}

func (r *rlReader) slashEsc() {
	tail, lone := readEscapeTail(func() (byte, bool) {
		return r.nextEscByte(r.escWait())
	})
	if lone {
		r.loneEsc()
		return
	}
	if arrowUp(tail) {
		r.slashUp()
		return
	}
	if arrowDown(tail) {
		r.slashDown()
		return
	}
	if dropEsc(tail) {
		return
	}
	action := r.actionFor(string(append([]byte{0x1b}, tail...)))
	if action == "" {
		action = fallbackEdit(tail)
	}
	if action == "" {
		return
	}
	r.callWidget(action)
	r.syncMenu()
}

func (r *rlReader) loneEsc() {
	r.syncMenu()
	if r.at != nil && r.at.open {
		r.applyMenu(r.at.Esc())
		return
	}
	if r.menu != nil {
		r.applyMenu(r.menu.Esc())
		return
	}
	r.applyLine("")
}

func (r *rlReader) slashTab() {
	r.syncMenu()
	if r.at != nil && r.at.Open() {
		res := r.at.Tab()
		if res.Action != actionPass {
			r.applyMenu(res)
		}
		return
	}
	line := ""
	if r.sh != nil && r.sh.Line() != nil {
		line = string(*r.sh.Line())
	}
	if strings.HasPrefix(line, "/") || (r.menu != nil && r.menu.Open()) {
		if r.menu != nil && r.menu.Open() {
			res := r.menu.Tab()
			if res.Action != actionPass {
				r.applyMenu(res)
			}
		}
		return
	}
	r.callWidget("complete")
}

func (r *rlReader) slashEnter() {
	r.syncMenu()
	if r.at != nil && r.at.Open() {
		res := r.at.Enter()
		if res.Action != actionPass {
			r.applyMenu(res)
			return
		}
	}
	if r.menu != nil && r.menu.Open() {
		res := r.menu.Enter()
		if res.Action != actionPass {
			r.applyMenu(res)
			return
		}
	}
	r.callWidget("accept-line")
}

func (r *rlReader) slashQuestion() {
	r.syncMenu()
	if r.at != nil && r.at.Open() {
		res := r.at.Question()
		if res.Action != actionPass {
			r.applyMenu(res)
			return
		}
	}
	if r.menu != nil && r.menu.Open() {
		res := r.menu.Question()
		if res.Action != actionPass {
			r.applyMenu(res)
			return
		}
	}
	r.callWidget("self-insert")
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

func (r *rlReader) SetOnClear(fn func()) { r.onClear = fn }

func (r *rlReader) clearScreen() {
	fmt.Fprint(os.Stdout, theme.ClearScroll)
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

func (r *rlReader) ReadKey() (byte, error) {
	if r == nil || r.sh == nil || r.sh.Keys == nil {
		return 0, io.EOF
	}
	for {
		if key, empty := r.sh.Keys.Pop(); !empty {
			if key == 0 {
				continue
			}
			return byte(key), nil
		}
		break
	}
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		st, err := term.MakeRaw(fd)
		if err == nil {
			defer func() { _ = term.Restore(fd, st) }()
		}
	}
	// One byte per call. Escape tails stay in the kernel buffer so the theme
	// picker can poll for them; a PTY rejects SetReadDeadline.
	var buf [1]byte
	n, err := os.Stdin.Read(buf[:])
	if n == 0 {
		if err != nil {
			return 0, err
		}
		return 0, io.EOF
	}
	return buf[0], nil
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

func (r *rlReader) SetFileRoot(cwd func() string) {
	if r != nil && r.at != nil {
		r.at.SetRoot(cwd)
	}
}

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
