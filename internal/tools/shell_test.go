package tools

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/siraj/konsolai/internal/safety"
	"github.com/siraj/konsolai/internal/ui"
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
