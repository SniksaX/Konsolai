package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/ui"
)

var ErrLookupMutating = errors.New("lookup can only run read-only commands")

type shellTool struct {
	guard          *safety.Guard
	out            io.Writer
	readOnlyOnly   bool
	docsTask       bool
	displayLines   int
	defaultTimeout time.Duration
	maxTimeout     time.Duration
	waitDelay      time.Duration
}

func newShell(g *safety.Guard, out io.Writer, readOnlyOnly, docsTask bool) *shellTool {
	return newShellLimit(g, out, readOnlyOnly, docsTask, 0)
}

func newShellLimit(g *safety.Guard, out io.Writer, readOnlyOnly, docsTask bool, displayLines int) *shellTool {
	b := config.Builtin()
	return &shellTool{
		guard: g, out: out, readOnlyOnly: readOnlyOnly, docsTask: docsTask, displayLines: displayLines,
		defaultTimeout: b.ShellDefaultTimeout(),
		maxTimeout:     b.ShellMaxTimeout(),
		waitDelay:      b.ShellWaitDelay(),
	}
}

func (t *shellTool) withLimits(def, max, wait time.Duration) *shellTool {
	if t == nil {
		return nil
	}
	if def > 0 {
		t.defaultTimeout = def
	}
	if max > 0 {
		t.maxTimeout = max
	}
	if wait > 0 {
		t.waitDelay = wait
	}
	return t
}

func (t *shellTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name: "run_shell",
			Description: "Run a bash command in a fresh process. Non-interactive only. " +
				"Each call is a new process; do not use cd; set the cwd argument instead (relative to the current directory). " +
				"Never start servers or watchers. " +
				"Avoid recursive listings (ls -R, find without -maxdepth, tree) over dependency dirs like node_modules, .git, dist, vendor.",
			Parameters: json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"cmd":{"type":"string","description":"Bash command to execute."},"cwd":{"type":"string","description":"Working directory relative to the current directory. Defaults to the current directory."},"timeout_sec":{"type":"integer","description":"Timeout in seconds. Default %d, maximum %d."}},"required":["cmd"]}`, int(t.defaultTimeout/time.Second), int(t.maxTimeout/time.Second))),
		},
	}
}

type shellArgs struct {
	Cmd        string `json:"cmd"`
	Cwd        string `json:"cwd"`
	TimeoutSec int    `json:"timeout_sec"`
}

type shellAction struct {
	cmd          string
	cwd          string
	timeout      time.Duration
	waitDelay    time.Duration
	out          io.Writer
	kind         ActionKind
	displayLines int
}

var reCD = regexp.MustCompile(`(^|[;&|(]\s*)cd(\s|$)`)

// rewriteLeadingCD turns a leading "cd DIR && rest" or "cd DIR; rest" into
// cwd=DIR and the remaining command. Other cd uses stay in the command.
func rewriteLeadingCD(cmd, cwd string) (string, string, bool) {
	s := strings.TrimLeft(cmd, " \t\n")
	if len(s) < 3 || !strings.HasPrefix(s, "cd") || (s[2] != ' ' && s[2] != '\t') {
		return cmd, cwd, false
	}
	i := 2
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	dir, next, ok := takeShellWord(s, i)
	if !ok || dir == "" || strings.HasPrefix(dir, "-") {
		return cmd, cwd, false
	}
	for next < len(s) && (s[next] == ' ' || s[next] == '\t') {
		next++
	}
	if next >= len(s) {
		return cmd, cwd, false
	}
	var sep int
	switch {
	case strings.HasPrefix(s[next:], "&&"):
		sep = 2
	case s[next] == ';':
		sep = 1
	default:
		return cmd, cwd, false
	}
	rest := strings.TrimSpace(s[next+sep:])
	if rest == "" {
		return cmd, cwd, false
	}
	base := strings.TrimSpace(cwd)
	if base == "" || base == "." {
		cwd = dir
	} else if filepath.IsAbs(dir) || strings.HasPrefix(dir, "~") {
		cwd = dir
	} else {
		cwd = filepath.Join(base, dir)
	}
	return rest, cwd, true
}

func takeShellWord(s string, i int) (string, int, bool) {
	if i >= len(s) {
		return "", i, false
	}
	if s[i] == '\'' || s[i] == '"' {
		q := s[i]
		j := i + 1
		for j < len(s) && s[j] != q {
			j++
		}
		if j >= len(s) {
			return "", i, false
		}
		return s[i+1 : j], j + 1, true
	}
	j := i
	for j < len(s) && s[j] != ' ' && s[j] != '\t' && s[j] != ';' && s[j] != '&' && s[j] != '|' && s[j] != '\n' {
		j++
	}
	if j == i {
		return "", i, false
	}
	return s[i:j], j, true
}

var shellQuietFor = time.Second

func (t *shellTool) Prepare(rawArgs string) (Action, error) {
	var args shellArgs
	dec := json.NewDecoder(strings.NewReader(rawArgs))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("tools: decode run_shell: %w", err)
	}
	cmd := strings.TrimSpace(args.Cmd)
	if cmd == "" {
		return nil, errors.New("tools: run_shell: cmd is required")
	}
	if rewritten, dir, ok := rewriteLeadingCD(cmd, args.Cwd); ok {
		cmd = rewritten
		args.Cwd = dir
	}
	if reCD.MatchString(cmd) {
		return nil, errors.New("do not use cd; set the cwd argument instead (relative to the current directory)")
	}
	if err := t.guard.CheckCommand(cmd); err != nil {
		return nil, err
	}
	if blocksExport(cmd) {
		return nil, errors.New("use the export_doc tool for diagrams and PDF/HTML export")
	}
	ro := safety.IsReadOnlyCommand(cmd)
	if t.readOnlyOnly && !ro {
		return nil, ErrLookupMutating
	}
	if t.docsTask && !ro && docsBlockedCmd(cmd) {
		return nil, errors.New(DocsSourceReject)
	}
	kind := Mutating
	if ro {
		kind = ReadOnly
	}
	cwd := args.Cwd
	if cwd == "" {
		cwd = "."
	}
	resolved, err := t.guard.ResolvePath(cwd)
	if err != nil {
		var denied *safety.DeniedError
		if errors.As(err, &denied) {
			return nil, fmt.Errorf("cwd outside the project: %s", cwd)
		}
		return nil, err
	}
	if err := t.guard.CheckCommandPaths(cmd, resolved); err != nil {
		return nil, err
	}
	sec := args.TimeoutSec
	if sec == 0 {
		sec = int(t.defaultTimeout / time.Second)
	}
	maxSec := int(t.maxTimeout / time.Second)
	if sec < 1 || sec > maxSec {
		return nil, fmt.Errorf("tools: run_shell: timeout_sec must be between 1 and %d", maxSec)
	}
	return &shellAction{cmd: cmd, cwd: resolved, timeout: time.Duration(sec) * time.Second, waitDelay: t.waitDelay, out: t.out, kind: kind, displayLines: t.displayLines}, nil
}

func (a *shellAction) Summary() string { return "run: " + CommandHeadline(a.cmd) }
func (a *shellAction) Preview() string {
	if strings.Count(strings.TrimRight(a.cmd, "\n"), "\n") == 0 {
		return ""
	}
	return a.cmd
}
func (a *shellAction) Kind() ActionKind    { return a.kind }
func (a *shellAction) PlanCommand() string { return a.cmd }

func (a *shellAction) Run(ctx context.Context) (Result, error) {
	if fi, err := os.Stat(a.cwd); err != nil || !fi.IsDir() {
		msg := fmt.Sprintf("cwd does not exist: %s", a.cwd)
		if err != nil && !os.IsNotExist(err) {
			msg = err.Error()
		}
		return Result{Output: msg, IsError: true, Meta: msg}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-o", "pipefail", "-c", a.cmd)
	cmd.Dir = a.cwd
	cmd.Stdin = nil
	cmd.Env = append(os.Environ(),
		"CI=1",
		"npm_config_yes=true",
		"GIT_TERMINAL_PROMPT=0",
		"DEBIAN_FRONTEND=noninteractive",
		"PAGER=cat",
		"GIT_PAGER=cat",
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = a.waitDelay
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	var cap Capture
	lim := NewLineLimiterN(a.out, a.displayLines)
	defer lim.Close(a.out)
	quiet := ui.NewQuiet("running", shellQuietFor)
	defer quiet.Stop()
	mw := io.MultiWriter(quiet.Tee(lim), &cap)
	cmd.Stdout = mw
	cmd.Stderr = mw

	runErr := cmd.Run()
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			sec := int(a.timeout.Seconds())
			return Result{
				Output:  fmt.Sprintf("timed out after %ds\nexit_code: %d\n%s", sec, exitCode(runErr), cap.String()),
				IsError: true,
				Meta:    fmt.Sprintf("timed out %ds", sec),
			}, nil
		}
		return Result{}, ctx.Err()
	}
	code := 0
	if runErr != nil {
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) {
			return Result{}, fmt.Errorf("tools: run_shell: %w", runErr)
		}
		code = ee.ExitCode()
	}
	if code == 141 {
		if cut, ok := pipelineCutBy(a.cmd); ok {
			return Result{
				Output:  fmt.Sprintf("exit_code: 141\n%s", cap.String()),
				IsError: false,
				Meta:    fmt.Sprintf("exit 141 (output cut by %s)", cut),
			}, nil
		}
	}
	return Result{
		Output:  fmt.Sprintf("exit_code: %d\n%s", code, cap.String()),
		IsError: code != 0,
		Meta:    fmt.Sprintf("exit %d", code),
	}, nil
}

// CommandHeadline is the first line of cmd, plus "(+N lines)" when multi-line.
func CommandHeadline(cmd string) string {
	lines := strings.Split(strings.TrimRight(cmd, "\n"), "\n")
	if len(lines) <= 1 {
		return cmd
	}
	return lines[0] + fmt.Sprintf(" (+%d lines)", len(lines)-1)
}

func pipelineCutBy(cmd string) (string, bool) {
	segs := safety.SplitSegments(cmd)
	if len(segs) < 2 {
		return "", false
	}
	last := strings.TrimSpace(segs[len(segs)-1])
	bin := safety.SegmentBinary(last)
	fields := strings.Fields(last)
	switch bin {
	case "head", "less":
		return bin, true
	case "tail":
		if hasDashFlag(fields, "-n") {
			return bin, true
		}
	case "grep":
		if hasDashFlag(fields, "-m") {
			return bin, true
		}
	case "sed":
		if sedNPrint(fields) {
			return bin, true
		}
	}
	return "", false
}

func hasDashFlag(fields []string, flag string) bool {
	for _, f := range fields {
		if f == flag || strings.HasPrefix(f, flag) {
			return true
		}
	}
	return false
}

func sedNPrint(fields []string) bool {
	sawN := false
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "-n") {
			sawN = true
			continue
		}
		if !sawN {
			continue
		}
		t := strings.Trim(f, `'"`)
		if len(t) < 2 || t[len(t)-1] != 'p' {
			continue
		}
		ok := true
		for _, c := range t[:len(t)-1] {
			if c < '0' || c > '9' {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

var exportBins = map[string]bool{
	"mmdc": true, "pandoc": true, "typst": true, "wkhtmltopdf": true, "weasyprint": true,
}

var browserBins = map[string]bool{
	"chromium": true, "chrome": true, "google-chrome": true, "google-chrome-stable": true, "firefox": true,
}

func docsBlockedCmd(cmd string) bool {
	for _, p := range []string{"go run", "go build", "node ", "npx ", "python", "tsx ", "internal/docrender"} {
		if strings.Contains(cmd, p) {
			return true
		}
	}
	return false
}

func blocksExport(cmd string) bool {
	for _, seg := range safety.SplitSegments(cmd) {
		bin := safety.SegmentBinary(seg)
		if exportBins[bin] {
			return true
		}
		if browserBins[bin] && (strings.Contains(seg, "--headless") || strings.Contains(seg, "--print-to-pdf")) {
			return true
		}
	}
	return false
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
