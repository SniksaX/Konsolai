package repl

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/reeflective/readline"
	"github.com/siraj/konsolai/internal/banner"
	"github.com/siraj/konsolai/internal/commands"
	"github.com/siraj/konsolai/internal/config"
	"github.com/siraj/konsolai/internal/repo"
	"github.com/siraj/konsolai/internal/safety"
	"github.com/siraj/konsolai/internal/shellmode"
	"github.com/siraj/konsolai/internal/store"
	"github.com/siraj/konsolai/internal/turn"
	"github.com/siraj/konsolai/internal/ui"
	"github.com/siraj/konsolai/internal/ui/theme"
	"golang.org/x/term"
)

type Config struct {
	Runner  *turn.Runner
	Env     *commands.Env
	Reg     *commands.Registry
	Reader  LineReader
	Version string
	Model   string
	Out     io.Writer
	Err     io.Writer
	Trace   bool
	Pastes  *PasteStore
}

type session struct {
	mu      sync.Mutex
	id      string
	cwd     string
	yes     bool
	trace   bool
	down    bool
	ctxPct  int
	showCtx bool
	shell   bool
	pastes  *PasteStore
	mode    *shellmode.Session
	cancel  context.CancelFunc
	warns   []string
}

type nlWriter struct {
	w    io.Writer
	last byte
	any  bool
}

func (n *nlWriter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		n.last = p[len(p)-1]
		n.any = true
	}
	return n.w.Write(p)
}

func (n *nlWriter) ensureNL() {
	if n.any && n.last != '\n' {
		_, _ = n.w.Write([]byte("\n"))
		n.last = '\n'
	}
}

func Run(ctx context.Context, cfg Config) int {
	out, errw := cfg.Out, cfg.Err
	if out == nil {
		out = os.Stdout
	}
	if errw == nil {
		errw = os.Stderr
	}
	origOut := out
	tracked := &nlWriter{w: out}
	out = tracked
	if cfg.Env != nil && (cfg.Env.Out == origOut || cfg.Env.Out == nil) {
		cfg.Env.Out = tracked
	}
	if cfg.Runner != nil && (cfg.Runner.Out == origOut || cfg.Runner.Out == nil) {
		cfg.Runner.Out = tracked
	}
	env := cfg.Env
	reg := cfg.Reg
	if reg == nil {
		reg = commands.Default()
	}
	cwd := env.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	pastes := cfg.Pastes
	if pastes == nil {
		pastes = NewPasteStore()
	}
	if env != nil {
		pastes.SetChipLimits(env.Profile.PasteChipLines(), env.Profile.PasteChipChars())
	}
	st := &session{cwd: cwd, yes: env.Yes, trace: cfg.Trace, pastes: pastes}
	st.mode = shellmode.New(cwd)
	st.mode.Stdout = tracked
	st.mode.Stderr = errw
	st.mode.Stdin = os.Stdin
	if g, err := safety.NewAt(repo.Root(cwd), cwd); err == nil {
		st.mode.Guard = g
	}
	env.InRepl = true
	env.Cwd = cwd
	wireEnv(env, st, cfg)

	p := ui.Current()
	model := cfg.Model
	if model == "" && env != nil {
		model = env.Profile.Model
	}
	if model == "" {
		model = "-"
	}
	info := banner.Info{
		Version: cfg.Version, Model: shortModel(model), Dir: shortPath(cwd),
		Branch: gitBranch(cwd),
	}
	if env != nil {
		info.Host = banner.HostOf(env.Profile.Endpoint)
	}
	if info.Model == "-" {
		info.Model = ""
	}
	tty := banner.IsTTY(origOut)
	if tty && env != nil && env.Profile.ClearOnStart() {
		fmt.Fprint(out, theme.ClearHome)
	}
	show := env == nil || env.Profile.ShowBanner()
	if err := banner.Startup(out, info, show, tty, p, banner.Width(origOut)); err != nil {
		fmt.Fprintln(errw, "konsolai:", err)
		return 1
	}

	go probeBackground(ctx, env.Profile, errw, st)

	if tty {
		if f, ok := origOut.(*os.File); ok {
			theme.DetectOnce(f)
		}
		if env != nil && env.Profile.CursorColorOn() && term.IsTerminal(int(os.Stdout.Fd())) {
			theme.SetCursorColor(true)
			theme.ApplyCursor(origOut)
			undoCur := theme.InstallCursorRestore(origOut)
			defer undoCur()
		}
	}

	rd := cfg.Reader
	if rd == nil {
		inner := Completer(reg, func() string { return st.cwd })
		completer := func(line []rune, cursor int) readline.Completions {
			st.mu.Lock()
			shell := st.shell
			st.mu.Unlock()
			if shell && st.mode != nil {
				return st.mode.Complete(string(line), cursor)
			}
			return inner(line, cursor)
		}
		var err error
		rd, err = NewReadline(completer, func() string {
			return sessionFooter(st, cfg)
		}, st.pastes, reg)
		if err != nil {
			fmt.Fprintln(errw, "konsolai:", err)
			return 1
		}
	}
	if kr, ok := rd.(interface{ ReadKey() (byte, error) }); ok {
		ui.SetKeyReader(kr.ReadKey)
		defer ui.SetKeyReader(nil)
	}
	if clr, ok := rd.(interface{ SetOnClear(func()) }); ok {
		clr.SetOnClear(func() {
			fmt.Fprintln(out, banner.Compact(info, ui.Current()))
		})
	}
	env.OnClear = func() {
		fmt.Fprintln(out, banner.Compact(info, ui.Current()))
	}
	if st.mode != nil {
		st.mode.OnClear = env.OnClear
	}
	defer func() { _ = rd.Close() }()
	setShell := func(on bool) {
		st.mu.Lock()
		st.shell = on
		st.mu.Unlock()
		if s, ok := rd.(interface{ SetShellMode(bool) }); ok {
			s.SetShellMode(on)
		}
	}
	env.OnCmd = func() int {
		st.mu.Lock()
		on := !st.shell
		st.mu.Unlock()
		setShell(on)
		return 0
	}
	env.OnCmdClear = func() {
		if st.mode != nil {
			st.mode.Hist.Clear()
		}
	}

	for {
		select {
		case <-ctx.Done():
			endSession(ctx, env, st)
			return 0
		default:
		}
		flushWarns(errw, st)
		ui.ResetDisplay()
		tracked.ensureNL()
		st.mu.Lock()
		shell := st.shell
		st.mu.Unlock()
		prompt := inputPrompt(shell)
		ui.HoldPrompt(true)
		line, err := rd.ReadLine(ctx, prompt)
		ui.HoldPrompt(false)
		if err != nil {
			if err == ErrEOF {
				if shell {
					setShell(false)
					continue
				}
				endSession(ctx, env, st)
				return 0
			}
			if err == ErrInterrupt {
				if strings.TrimSpace(line) == "" {
					clearEmptyPrompt(origOut)
					fmt.Fprintln(out, p.Muted("press Ctrl+D or type /exit to quit"))
				}
				continue
			}
			fmt.Fprintln(errw, "konsolai:", err)
			return 1
		}
		line = strings.TrimRight(line, "\n")
		if strings.TrimSpace(line) == "" {
			continue
		}
		line = st.pastes.Expand(line)
		if fe, ok := rd.(interface{ EraseFrame(io.Writer, string) }); ok && tty {
			fe.EraseFrame(origOut, line)
		}
		echoInput(out, line)
		fmt.Fprintln(out)
		trim := strings.TrimSpace(line)
		if trim == "?" {
			line = "/help"
			trim = "/help"
		}
		if shell {
			if trim == "/cmd" || trim == "exit" {
				setShell(false)
				fmt.Fprintln(out)
				continue
			}
			if strings.HasPrefix(trim, "/") {
				code := dispatchLine(ctx, reg, env, line)
				if code == -1 {
					endSession(ctx, env, st)
					return 0
				}
				fmt.Fprintln(out)
				continue
			}
			toAI, _ := st.mode.Handle(ctx, line)
			if toAI {
				setShell(false)
			}
			fmt.Fprintln(out)
			continue
		}
		if strings.HasPrefix(trim, "!") {
			_, _ = st.mode.Handle(ctx, strings.TrimPrefix(trim, "!"))
			fmt.Fprintln(out)
			continue
		}
		if strings.HasPrefix(trim, "/") {
			code := dispatchLine(ctx, reg, env, line)
			if code == -1 {
				endSession(ctx, env, st)
				return 0
			}
			fmt.Fprintln(out)
			continue
		}
		if st.mode != nil && st.mode.Hist.Len() > 0 {
			n := st.mode.Hist.Len()
			noun := "commands"
			if n == 1 {
				noun = "command"
			}
			fmt.Fprintln(out, p.Muted(fmt.Sprintf("including %d shell %s as context", n, noun)))
			line = st.mode.Hist.Format() + line
			st.mode.Hist.Clear()
		}
		turnCtx, cancel := signal.NotifyContext(ctx, os.Interrupt)
		st.mu.Lock()
		st.cancel = cancel
		st.mu.Unlock()
		res := cfg.Runner.RunTurn(turnCtx, turn.Request{
			Text: line, Yes: st.yes, Trace: st.trace, Cwd: st.cwd,
			ResumeSession: st.id, New: st.id == "", KeepOpen: true,
		})
		cancelled := res.ExitCode == 130 || (turnCtx.Err() != nil && ctx.Err() == nil)
		cancel()
		st.mu.Lock()
		st.cancel = nil
		if res.SessionID != "" {
			st.id = res.SessionID
		}
		if res.Window > 0 && res.PromptTokens > 0 {
			st.ctxPct = int(float64(res.PromptTokens) / float64(res.Window) * 100)
			st.showCtx = true
		}
		st.mu.Unlock()
		if cancelled {
			fmt.Fprintln(out, p.Muted("cancelled"))
		}
		fmt.Fprintln(out)
	}
}

func dispatchLine(ctx context.Context, reg *commands.Registry, env *commands.Env, line string) int {
	line = strings.TrimSpace(line)
	name, args := splitCmd(line)
	if name == "exit" || name == "quit" {
		return -1
	}
	return reg.Dispatch(ctx, env, name, args)
}

func splitCmd(line string) (string, []string) {
	line = strings.TrimPrefix(line, "/")
	var parts []string
	var cur strings.Builder
	inQ := false
	for _, r := range line {
		switch {
		case r == '"':
			inQ = !inQ
		case unicodeSpace(r) && !inQ:
			if cur.Len() > 0 {
				parts = append(parts, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	if len(parts) == 0 {
		return "", nil
	}
	return parts[0], parts[1:]
}

func unicodeSpace(r rune) bool { return r == ' ' || r == '\t' }

func clearEmptyPrompt(w io.Writer) {
	f, ok := w.(*os.File)
	if !ok {
		return
	}
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return
	}
	fmt.Fprint(w, theme.CursorUp(1)+theme.ClearLine)
}

func echoInput(w io.Writer, text string) {
	limit := theme.TermWidth() - 4
	if limit < 8 {
		limit = 8
	}
	text = theme.Truncate(text, limit)
	fmt.Fprintln(w, "  "+theme.Style(theme.Accent, "›")+" "+theme.Bold(text))
}

func sessionFooter(st *session, cfg Config) string {
	st.mu.Lock()
	cwd, down, ctxPct, showCtx, shell := st.cwd, st.down, st.ctxPct, st.showCtx, st.shell
	st.mu.Unlock()
	routing := "main"
	if cfg.Runner != nil && cfg.Runner.RouterSource != "" {
		routing = cfg.Runner.RouterSource
	} else if cfg.Env != nil && cfg.Env.Profile.FastEndpoint != "" {
		routing = "fast"
	}
	model := cfg.Model
	if model == "" && cfg.Env != nil {
		model = cfg.Env.Profile.Model
	}
	return formatFooter(footerInfo{
		Cwd: cwd, Model: model, Routing: routing,
		CtxPct: ctxPct, ShowCtx: showCtx, Down: down, Shell: shell,
	})
}

func wireEnv(env *commands.Env, st *session, cfg Config) {
	env.OnCwd = func(dir string) error {
		abs, err := expandDir(st.cwd, dir)
		if err != nil {
			return err
		}
		st.cwd = abs
		env.Cwd = abs
		if st.mode != nil {
			st.mode.Cwd = abs
			if st.mode.Guard != nil {
				_ = st.mode.Guard.SetBase(abs)
			}
		}
		return nil
	}
	env.OnYes = func(on bool) { st.yes = on; env.Yes = on }
	env.OnTrace = func(on bool) { st.trace = on }
	env.OnNewSession = func() {
		endSession(context.Background(), env, st)
		st.id = ""
	}
	env.OnCompact = func(ctx context.Context) int {
		return runCompact(ctx, cfg.Runner, env, st)
	}
	env.OnExit = func() int { return -1 }
}

func expandDir(cwd, dir string) (string, error) {
	if strings.HasPrefix(dir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if dir == "~" {
			dir = home
		} else if strings.HasPrefix(dir, "~/") {
			dir = filepath.Join(home, dir[2:])
		}
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cwd, dir)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("not a directory")
	}
	return abs, nil
}

func endSession(ctx context.Context, env *commands.Env, st *session) {
	if st.id == "" || env.Mem == nil || env.Mem.Store() == nil {
		return
	}
	endCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_ = env.Mem.Store().EndSession(endCtx, st.id, "finished")
}

func runCompact(ctx context.Context, r *turn.Runner, env *commands.Env, st *session) int {
	if r == nil || r.Client == nil {
		fmt.Fprintln(env.Err, "konsolai: no model client")
		return 1
	}
	hist := turn.LoadCompacted(ctx, env.Mem, st.id, env.Profile.Name, st.cwd)
	if len(hist) == 0 {
		fmt.Fprintln(env.Out, "nothing to compact")
		return 0
	}
	win := r.Client.ContextWindow()
	before := r.Client.EstimatePromptTokens(hist, nil)
	sum, upto, err := turn.CompactMessages(ctx, r.Client, hist)
	if err != nil {
		fmt.Fprintln(env.Err, "konsolai:", err)
		return 1
	}
	if sum == "" {
		fmt.Fprintln(env.Out, "nothing to compact")
		return 0
	}
	turn.PersistCompact(ctx, env.Mem, st.id, env.Profile.Name, st.cwd, sum, upto, hist)
	if r.Ingest != nil {
		r.Ingest.EnqueueSummary(turn.UpsertProject(ctx, env.Mem, st.cwd), st.id, sum, upto)
	}
	after := r.Client.EstimatePromptTokens(turn.HistoryWithSummary(hist, sum, upto), nil)
	turn.PrintCompacted(env.Out, upto, before, after, win)
	return 0
}

func probeBackground(ctx context.Context, prof config.Profile, errw io.Writer, st *session) {
	p := ui.Current()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if prof.Endpoint == "" {
			markDown(st, p.Warn("endpoint unreachable"))
			return
		}
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(cctx, http.MethodGet, strings.TrimRight(prof.Endpoint, "/")+"/models", nil)
		if err != nil {
			markDown(st, p.Warn("endpoint unreachable"))
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			markDown(st, p.Warn("endpoint unreachable"))
			return
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 400 {
			markDown(st, p.Warn("endpoint unreachable"))
		}
	}()
	go func() {
		defer wg.Done()
		if prof.DatabaseURL == "" {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		s, err := store.Open(cctx, prof.DatabaseURL)
		if err != nil {
			addWarn(st, p.Warn("database unreachable"))
			return
		}
		s.Close()
	}()
	wg.Wait()
}

func markDown(st *session, msg string) {
	st.mu.Lock()
	st.down = true
	st.warns = append(st.warns, msg)
	st.mu.Unlock()
}

func addWarn(st *session, msg string) {
	st.mu.Lock()
	st.warns = append(st.warns, msg)
	st.mu.Unlock()
}

func flushWarns(w io.Writer, st *session) {
	st.mu.Lock()
	msgs := st.warns
	st.warns = nil
	st.mu.Unlock()
	for _, m := range msgs {
		fmt.Fprintln(w, m)
	}
}
