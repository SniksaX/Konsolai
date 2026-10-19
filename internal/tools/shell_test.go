package tools

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/ui"
)

func shellToolFor(t *testing.T) Tool {
	t.Helper()
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := NewRegistry(g, io.Discard, nil, nil, nil).Get("run_shell")
	if !ok {
		t.Fatal("missing run_shell")
	}
	return tool
}

func TestShellExitCode(t *testing.T) {
	act, err := shellToolFor(t).Prepare(`{"cmd":"exit 2"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("want IsError")
	}
	if res.Meta != "exit 2" {
		t.Fatalf("Meta %q", res.Meta)
	}
	if !strings.Contains(res.Output, "exit_code: 2") {
		t.Fatalf("Output %q", res.Output)
	}
}

func TestShellTimeout(t *testing.T) {
	act, err := shellToolFor(t).Prepare(`{"cmd":"sleep 5","timeout_sec":1}`)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res, err := act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("timeout took too long")
	}
	if !res.IsError {
		t.Fatal("want IsError")
	}
	if !strings.Contains(res.Output, "timed out") {
		t.Fatalf("Output %q", res.Output)
	}
	if res.Meta != "timed out 1s" {
		t.Fatalf("Meta %q", res.Meta)
	}
}

func TestShellKindReadOnly(t *testing.T) {
	act, err := shellToolFor(t).Prepare(`{"cmd":"go version"}`)
	if err != nil {
		t.Fatal(err)
	}
	if act.Kind() != ReadOnly {
		t.Fatalf("kind %s", act.Kind())
	}
}

func TestShellKindMutating(t *testing.T) {
	act, err := shellToolFor(t).Prepare(`{"cmd":"echo hi > out"}`)
	if err != nil {
		t.Fatal(err)
	}
	if act.Kind() != Mutating {
		t.Fatalf("kind %s", act.Kind())
	}
}

func TestShellLookupRejectsMutating(t *testing.T) {
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := NewRegistry(g, io.Discard, nil, nil, nil, Options{ReadOnlyShell: true}).Get("run_shell")
	if !ok {
		t.Fatal("missing run_shell")
	}
	if _, err := tool.Prepare(`{"cmd":"echo hi"}`); err != nil {
		t.Fatalf("readonly: %v", err)
	}
	_, err = tool.Prepare(`{"cmd":"rm x"}`)
	if err == nil || !strings.Contains(err.Error(), "lookup can only run read-only commands") {
		t.Fatalf("got %v", err)
	}
}

func TestShellBlocksExportRenderers(t *testing.T) {
	tool := shellToolFor(t)
	blocked := []string{
		"mmdc -i diagram.mmd -o out.png",
		"pandoc doc.md -o doc.pdf",
		"typst compile doc.typ",
		"wkhtmltopdf page.html out.pdf",
		"weasyprint page.html out.pdf",
		"chromium --headless --print-to-pdf=out.pdf page.html",
		"chrome --print-to-pdf=out.pdf",
		"google-chrome --headless --dump-dom",
		"google-chrome-stable --print-to-pdf",
		"firefox --headless --screenshot",
	}
	for _, cmd := range blocked {
		_, err := tool.Prepare(`{"cmd":` + jsonQuote(cmd) + `}`)
		if err == nil || !strings.Contains(err.Error(), "use the export_doc tool for diagrams and PDF/HTML export") {
			t.Errorf("%q: %v", cmd, err)
		}
	}
	if _, err := tool.Prepare(`{"cmd":"cat pandoc.md"}`); err != nil {
		t.Fatalf("lookalike: %v", err)
	}
	if _, err := tool.Prepare(`{"cmd":"echo mmdc"}`); err != nil {
		t.Fatalf("echo lookalike: %v", err)
	}
}

func TestDocsTaskRejectsBuildCommands(t *testing.T) {
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := NewRegistry(g, io.Discard, nil, nil, nil, Options{DocsTask: true}).Get("run_shell")
	if !ok {
		t.Fatal("missing run_shell")
	}
	blocked := []string{
		"go run ./cmd/render_overview",
		"go build ./cmd/foo",
		"node ./render.js",
		"npx mmdc -i x.mmd",
		"python3 app.py",
		"tsx src/render.ts",
		"rm internal/docrender/foo.go",
	}
	for _, cmd := range blocked {
		_, err := tool.Prepare(`{"cmd":` + jsonQuote(cmd) + `}`)
		if err == nil || err.Error() != DocsSourceReject {
			t.Errorf("%q: %v", cmd, err)
		}
	}
	if _, err := tool.Prepare(`{"cmd":"cat internal/docrender/render.go"}`); err != nil {
		t.Fatalf("readonly docrender: %v", err)
	}
	if _, err := tool.Prepare(`{"cmd":"go version"}`); err != nil {
		t.Fatalf("readonly go: %v", err)
	}
	if _, err := tool.Prepare(`{"cmd":"ls"}`); err != nil {
		t.Fatalf("ls: %v", err)
	}
	plain, _ := NewRegistry(g, io.Discard, nil, nil, nil).Get("run_shell")
	if _, err := plain.Prepare(`{"cmd":"go build ./cmd/foo"}`); err != nil {
		t.Fatalf("non-docs build: %v", err)
	}
}

func TestShellCancelKillsGrandchild(t *testing.T) {
	act, err := shellToolFor(t).Prepare(`{"cmd":"sleep 30 & wait"}`)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, runErr := act.Run(ctx)
		done <- runErr
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case runErr := <-done:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("got %v", runErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not return quickly")
	}
}

func TestShellRunningSpinner(t *testing.T) {
	var buf bytes.Buffer
	restore := ui.TestHook(&buf, true, 5*time.Millisecond, nil)
	defer restore()
	old := shellQuietFor
	shellQuietFor = 20 * time.Millisecond
	defer func() { shellQuietFor = old }()
	act, err := shellToolFor(t).Prepare(`{"cmd":"sleep 0.08"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := act.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "running") || !strings.Contains(buf.String(), "\033[?25l") {
		t.Fatalf("spinner %q", buf.String())
	}
}

func TestShellSpinnerClearedBeforeOutput(t *testing.T) {
	var buf bytes.Buffer
	restore := ui.TestHook(&buf, true, time.Millisecond, nil)
	defer restore()
	old := shellQuietFor
	shellQuietFor = 5 * time.Millisecond
	defer func() { shellQuietFor = old }()
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	act, err := newShell(g, &buf, false, false).Prepare(`{"cmd":"sleep 0.04; printf 'hello\nworld\n'"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := act.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	for _, line := range committedShellLines(buf.String()) {
		for _, f := range frames {
			if strings.HasPrefix(strings.TrimLeft(line, " "), f) {
				t.Fatalf("output line starts with spinner frame %q: %q (raw %q)", f, line, buf.String())
			}
		}
	}
}

func committedShellLines(s string) []string {
	var cur []byte
	var lines []string
	for i := 0; i < len(s); i++ {
		if s[i] == '\033' {
			j := i + 1
			for j < len(s) && (s[j] < 'A' || s[j] > 'Z') && (s[j] < 'a' || s[j] > 'z') {
				j++
			}
			if j < len(s) && s[j] == 'K' {
				cur = cur[:0]
			}
			if j < len(s) {
				i = j
			}
			continue
		}
		if s[i] == '\r' {
			cur = cur[:0]
			continue
		}
		if s[i] == '\n' {
			lines = append(lines, string(cur))
			cur = cur[:0]
			continue
		}
		cur = append(cur, s[i])
	}
	if len(cur) > 0 {
		lines = append(lines, string(cur))
	}
	return lines
}

func TestCommandHeadlineAndPreview(t *testing.T) {
	if CommandHeadline("echo hi") != "echo hi" {
		t.Fatalf("%q", CommandHeadline("echo hi"))
	}
	if CommandHeadline("echo a\necho b\necho c") != "echo a (+2 lines)" {
		t.Fatalf("%q", CommandHeadline("echo a\necho b\necho c"))
	}
	act, err := shellToolFor(t).Prepare("{\"cmd\":\"echo a\\necho b\"}")
	if err != nil {
		t.Fatal(err)
	}
	if act.Summary() != "run: echo a (+1 lines)" {
		t.Fatalf("summary %q", act.Summary())
	}
	if act.Preview() != "echo a\necho b" {
		t.Fatalf("preview %q", act.Preview())
	}
	act, err = shellToolFor(t).Prepare(`{"cmd":"echo hi"}`)
	if err != nil {
		t.Fatal(err)
	}
	if act.Preview() != "" {
		t.Fatalf("single-line preview %q", act.Preview())
	}
}

func TestPipelineCutBy(t *testing.T) {
	if cut, ok := pipelineCutBy("yes | head -3"); !ok || cut != "head" {
		t.Fatalf("head %q %v", cut, ok)
	}
	if cut, ok := pipelineCutBy("yes | tail -n 2"); !ok || cut != "tail" {
		t.Fatalf("tail %q %v", cut, ok)
	}
	if cut, ok := pipelineCutBy("yes | grep -m 1 x"); !ok || cut != "grep" {
		t.Fatalf("grep %q %v", cut, ok)
	}
	if cut, ok := pipelineCutBy("yes | sed -n 1p"); !ok || cut != "sed" {
		t.Fatalf("sed %q %v", cut, ok)
	}
	if _, ok := pipelineCutBy("head -3"); ok {
		t.Fatal("no pipeline")
	}
	if _, ok := pipelineCutBy("yes | wc -l"); ok {
		t.Fatal("wc is not a cutter")
	}
}

func TestShellExit141HeadSuccess(t *testing.T) {
	act, err := shellToolFor(t).Prepare(`{"cmd":"yes | head -3"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("want success: %+v", res)
	}
	if res.Meta != "exit 141 (output cut by head)" {
		t.Fatalf("Meta %q", res.Meta)
	}
	if !strings.Contains(res.Output, "exit_code: 141") {
		t.Fatalf("Output %q", res.Output)
	}
}

func TestShellExit141BareIsError(t *testing.T) {
	act, err := shellToolFor(t).Prepare(`{"cmd":"exit 141"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || res.Meta != "exit 141" {
		t.Fatalf("%+v", res)
	}
}
