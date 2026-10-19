package repl

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/creack/pty"
)

func TestThemePickerEscNoExtraBanner(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == "theme-esc" {
		themePickerChild()
		return
	}
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	raw, cfg := driveThemePicker(t, "TestThemePickerEscNoExtraBanner", "theme-esc", "\x1b[B\x1b[B\x1b")
	if bytes.Contains(raw, []byte("\x1b[3J")) {
		t.Fatalf("esc cleared the screen:\n%s", ansiRe.ReplaceAll(raw, nil))
	}
	sc := newScreen(48, 120)
	sc.feed(raw)
	if n := blockLines(sc); n < 1 || n > 5 {
		t.Fatalf("banner rows %d (want one wordmark)\n%s", n, sc.dump())
	}
	if strings.Contains(sc.dump(), "◆ build") || strings.Contains(sc.dump(), "glacier") {
		t.Fatalf("picker still on screen:\n%s", sc.dump())
	}
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "theme: amber") || strings.Contains(string(body), "glacier") || strings.Contains(string(body), "nebula") {
		t.Fatalf("theme changed:\n%s", body)
	}
}

func TestThemePickerEnterAppliesInPlace(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == "theme-enter" {
		themePickerChild()
		return
	}
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	raw, cfg := driveThemePicker(t, "TestThemePickerEnterAppliesInPlace", "theme-enter", "\x1b[B\r")
	if n := bytes.Count(raw, []byte("\x1b[H\x1b[2J")); n != 1 || bytes.Contains(raw, []byte("\x1b[3J")) {
		t.Fatalf("enter cleared the screen (%d home-clears):\n%s", n, ansiRe.ReplaceAll(raw, nil))
	}
	sc := newScreen(48, 120)
	sc.feed(raw)
	if n := blockLines(sc); n < 1 || n > 5 {
		t.Fatalf("wordmark rows %d (welcome was redrawn)\n%s", n, sc.dump())
	}
	plain := sc.dump()
	if strings.Contains(plain, "◆ build") || strings.Contains(plain, "glacier") {
		t.Fatalf("picker still on screen:\n%s", plain)
	}
	if !strings.Contains(plain, "main-model") {
		t.Fatalf("welcome left the screen:\n%s", plain)
	}
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "theme: glacier") {
		t.Fatalf("not saved:\n%s", body)
	}
}

func themePickerChild() {
	dir := os.Getenv("KONSOLAI_THEME_CWD")
	cfgPath := os.Getenv("KONSOLAI_THEME_CFG")
	prof := config.Builtin()
	prof.SetMain("http://127.0.0.1:1/v1", "main-model", false)
	prof.Roles.Fast = ""
	prof.Roles.Embed = ""
	prof.Roles.Rerank = ""
	prof.RAG.Enabled = false
	prof.Database.URL = ""
	prof.UI.ClearOnStart = false
	prof.UI.Banner = true
	prof.UI.Welcome = "split"
	env := &commands.Env{
		Profile:    prof,
		Out:        os.Stdout,
		Err:        os.Stderr,
		Cwd:        dir,
		Version:    "vtest",
		ConfigPath: cfgPath,
	}
	code := Run(context.Background(), Config{
		Env: env, Reg: commands.Default(), Version: "vtest", Model: "main-model",
		Out: os.Stdout, Err: os.Stderr,
	})
	os.Exit(code)
}

func driveThemePicker(t *testing.T, testName, child, keys string) ([]byte, string) {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "src", "app")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	init := exec.Command("git", "init", "-b", "dev", dir)
	init.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("defaults:\n  ui:\n    theme: amber\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$")
	cmd.Env = append(os.Environ(),
		"KONSOLAI_PTY_CHILD="+child,
		"HOME="+home,
		"KONSOLAI_THEME_CWD="+dir,
		"KONSOLAI_THEME_CFG="+cfg,
		"COLUMNS=120",
		"LINES=48",
		"INPUTRC=/dev/null",
	)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = ptmx.Close()
		_ = cmd.Wait()
	}()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 48, Cols: 120})
	var mu sync.Mutex
	var buf bytes.Buffer
	go func() {
		tmp := make([]byte, 4096)
		for {
			n, err := ptmx.Read(tmp)
			if n > 0 {
				mu.Lock()
				buf.Write(tmp[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	waitPlain := func(needle string, d time.Duration) string {
		t.Helper()
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			mu.Lock()
			plain := ansiRe.ReplaceAllString(strings.ReplaceAll(buf.String(), "\r", ""), "")
			mu.Unlock()
			if strings.Contains(plain, needle) {
				return plain
			}
			time.Sleep(20 * time.Millisecond)
		}
		mu.Lock()
		plain := ansiRe.ReplaceAllString(strings.ReplaceAll(buf.String(), "\r", ""), "")
		mu.Unlock()
		t.Fatalf("timeout waiting for %q\n%s", needle, plain)
		return ""
	}
	waitPlain("main-model", 3*time.Second)
	time.Sleep(150 * time.Millisecond)
	if _, err := ptmx.Write([]byte("/theme\r")); err != nil {
		t.Fatal(err)
	}
	waitPlain("mono", 3*time.Second)
	time.Sleep(80 * time.Millisecond)
	if _, err := ptmx.Write([]byte(keys)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(keys, "\r") {
		waitPlain("applied now", 3*time.Second)
	}
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	out := append([]byte(nil), buf.Bytes()...)
	mu.Unlock()
	return out, cfg
}

func blockLines(sc *screen) int {
	n := 0
	for _, line := range sc.lines() {
		if strings.Contains(line, "█") {
			n++
		}
	}
	return n
}
