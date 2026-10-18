package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
)

var errNoChrome = errors.New("chromium missing")

func answerEnv(t *testing.T) (*Env, *strings.Builder, *strings.Builder) {
	t.Helper()
	env, outb, errb := testEnv(t)
	var out, err strings.Builder
	env.Out = &out
	env.Err = &err
	env.LoadHistory = func(context.Context, int) []histItem {
		return []histItem{{
			Title: "Haiku About Terminals",
			Msgs: []llm.Message{
				{Role: llm.RoleUser, Content: "q"},
				{Role: llm.RoleAssistant, Content: "line one\nline two\nline three\n"},
			},
		}}
	}
	t.Cleanup(func() { _, _ = outb, errb })
	return env, &out, &err
}

func TestSaveDefaultNameAndOverwrite(t *testing.T) {
	env, out, errb := answerEnv(t)
	if code := runSave(context.Background(), env, nil); code != 0 {
		t.Fatalf("save %d %s", code, errb.String())
	}
	name := "haiku-about-terminals.md"
	path := filepath.Join(env.Cwd, name)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "line one\nline two\nline three\n" {
		t.Fatalf("body %q", body)
	}
	if !strings.Contains(out.String(), "saved "+name+" (") {
		t.Fatalf("%q", out.String())
	}
	out.Reset()
	if code := runSave(context.Background(), env, nil); code != 1 {
		t.Fatalf("overwrite %d", code)
	}
	if !strings.Contains(errb.String(), "refusing to overwrite") || !strings.Contains(errb.String(), "--force") {
		t.Fatalf("%q", errb.String())
	}
	errb.Reset()
	if code := runSave(context.Background(), env, []string{"--force"}); code != 0 {
		t.Fatalf("force %d %s", code, errb.String())
	}
}

func TestSaveGuardDeniesEscape(t *testing.T) {
	env, _, errb := answerEnv(t)
	if code := runSave(context.Background(), env, []string{"/etc/passwd"}); code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(errb.String(), "denied") {
		t.Fatalf("denial %q", errb.String())
	}
}

func TestPDFSkippedWithoutChrome(t *testing.T) {
	prev := renderChrome
	t.Cleanup(func() { renderChrome = prev })
	renderChrome = func() (string, error) { return "", errNoChrome }
	env, _, errb := answerEnv(t)
	if code := runPDF(context.Background(), env, nil); code != 1 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "pdf skipped") {
		t.Fatalf("%q", errb.String())
	}
	matches, _ := filepath.Glob(filepath.Join(env.Cwd, "*.pdf"))
	if len(matches) != 0 {
		t.Fatalf("wrote %v", matches)
	}
}
