package tools

import (
	"strings"
	"testing"
	"time"

	"github.com/siraj/konsolai/internal/safety"
)

func TestShellDefaultSpecKeepsBuiltinNumbers(t *testing.T) {
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := NewRegistry(g, nil, nil, nil, nil).Get("run_shell")
	if !ok {
		t.Fatal("missing run_shell")
	}
	spec := string(tool.Spec().Function.Parameters)
	if !strings.Contains(spec, "Default 120, maximum 600.") {
		t.Fatalf("spec %s", spec)
	}
}

func TestShellLimitsFollowOptions(t *testing.T) {
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := NewRegistry(g, nil, nil, nil, nil, Options{
		ShellDefaultTimeout: 30 * time.Second,
		ShellMaxTimeout:     40 * time.Second,
		ShellWaitDelay:      15 * time.Millisecond,
	}).Get("run_shell")
	if !ok {
		t.Fatal("missing run_shell")
	}
	act, err := tool.Prepare(`{"cmd":"true"}`)
	if err != nil {
		t.Fatal(err)
	}
	got := act.(*shellAction)
	if got.timeout != 30*time.Second {
		t.Fatalf("default timeout %s", got.timeout)
	}
	if got.waitDelay != 15*time.Millisecond {
		t.Fatalf("wait delay %s", got.waitDelay)
	}
	if _, err := tool.Prepare(`{"cmd":"true","timeout_sec":41}`); err == nil {
		t.Fatal("timeout above the override max was accepted")
	}
	act, err = tool.Prepare(`{"cmd":"true","timeout_sec":40}`)
	if err != nil {
		t.Fatal(err)
	}
	if act.(*shellAction).timeout != 40*time.Second {
		t.Fatalf("max timeout %s", act.(*shellAction).timeout)
	}
}
