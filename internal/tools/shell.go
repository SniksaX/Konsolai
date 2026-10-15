package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/safety"
	"github.com/siraj/konsolai/internal/ui"
)

type shellTool struct {
	guard        *safety.Guard
	out          io.Writer
	readOnlyOnly bool
}

func newShell(g *safety.Guard, out io.Writer, readOnlyOnly bool) *shellTool {
	return &shellTool{guard: g, out: out, readOnlyOnly: readOnlyOnly}
}

func (t *shellTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name: "run_shell",
			Description: "Run a bash command in a fresh process. Non-interactive only. " +
				"Each call is a new process; do not use cd; set the cwd argument instead (relative to the project root). " +
				"Never start servers or watchers. " +
				"Avoid recursive listings (ls -R, find without -maxdepth, tree) over dependency dirs like node_modules, .git, dist, vendor.",
			Parameters: json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string","description":"Bash command to execute."},"cwd":{"type":"string","description":"Working directory relative to the project root. Defaults to the root."},"timeout_sec":{"type":"integer","description":"Timeout in seconds. Default 120, maximum 600."}},"required":["cmd"]}`),
		},
	}
}

type shellArgs struct {
	Cmd        string `json:"cmd"`
	Cwd        string `json:"cwd"`
	TimeoutSec int    `json:"timeout_sec"`
}

type shellAction struct {
	cmd     string
	cwd     string
	timeout time.Duration
	out     io.Writer
	kind    ActionKind
}

var reCD = regexp.MustCompile(`(^|[;&|(]\s*)cd(\s|$)`)

var shellQuietFor = time.Second

func (t *shellTool) Prepare(rawArgs string) (Action, error) {
	var args shellArgs
	dec := json.NewDecoder(strings.NewReader(rawArgs))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("tools: decode run_shell: %w", err)
	}
	if strings.TrimSpace(args.Cmd) == "" {
		return nil, errors.New("tools: run_shell: cmd is required")
	}
	if reCD.MatchString(args.Cmd) {
		return nil, errors.New("do not use cd; set the cwd argument instead (relative to the project root)")
	}
	if err := t.guard.CheckCommand(args.Cmd); err != nil {
		return nil, err
	}
	if blocksExport(args.Cmd) {
		return nil, errors.New("use the export_doc tool for diagrams and PDF/HTML export")
	}
	ro := safety.IsReadOnlyCommand(args.Cmd)
	if t.readOnlyOnly && !ro {
		return nil, errors.New("lookup can only run read-only commands")
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
		return nil, err
	}
	sec := args.TimeoutSec
	if sec == 0 {
		sec = 120
	}
	if sec < 1 || sec > 600 {
		return nil, fmt.Errorf("tools: run_shell: timeout_sec must be between 1 and 600")
	}
	return &shellAction{cmd: args.Cmd, cwd: resolved, timeout: time.Duration(sec) * time.Second, out: t.out, kind: kind}, nil
}

func (a *shellAction) Summary() string     { return "run: " + a.cmd }
func (a *shellAction) Preview() string     { return "" }
func (a *shellAction) Kind() ActionKind    { return a.kind }
func (a *shellAction) PlanCommand() string { return a.cmd }

func (a *shellAction) Run(ctx context.Context) (Result, error) {
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
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	var cap Capture
	lim := NewLineLimiter(a.out)
	defer lim.Close(a.out)
	quiet := ui.NewQuiet("running", shellQuietFor)
	defer quiet.Stop()
	mw := io.MultiWriter(lim, &cap, quiet)
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
	return Result{
		Output:  fmt.Sprintf("exit_code: %d\n%s", code, cap.String()),
		IsError: code != 0,
		Meta:    fmt.Sprintf("exit %d", code),
	}, nil
}

var exportBins = map[string]bool{
	"mmdc": true, "pandoc": true, "typst": true, "wkhtmltopdf": true, "weasyprint": true,
}

var browserBins = map[string]bool{
	"chromium": true, "chrome": true, "google-chrome": true, "google-chrome-stable": true, "firefox": true,
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
