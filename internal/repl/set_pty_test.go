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

func TestSetMenuPicksEnum(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == "set-menu" {
		setMenuChild()
		return
	}
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
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
	if err := os.WriteFile(cfg, []byte("defaults:\n  ui:\n    welcome: split\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSetMenuPicksEnum$")
	cmd.Env = append(os.Environ(),
		"KONSOLAI_PTY_CHILD=set-menu",
		"HOME="+home,
		"KONSOLAI_THEME_CWD="+dir,
		"KONSOLAI_THEME_CFG="+cfg,
		"COLUMNS=120",
		"LINES=48",
		"INPUTRC=/dev/null",
		"NO_COLOR=1",
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
	waitPlain := func(needle string, d time.Duration) {
		t.Helper()
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			mu.Lock()
			plain := ansiRe.ReplaceAllString(strings.ReplaceAll(buf.String(), "\r", ""), "")
			mu.Unlock()
			if strings.Contains(plain, needle) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		mu.Lock()
		plain := ansiRe.ReplaceAllString(strings.ReplaceAll(buf.String(), "\r", ""), "")
		mu.Unlock()
		t.Fatalf("timeout waiting for %q\n%s", needle, plain)
	}
	waitPlain("main-model", 4*time.Second)
	time.Sleep(150 * time.Millisecond)
	if _, err := ptmx.Write([]byte("/set\r")); err != nil {
		t.Fatal(err)
	}
	waitPlain("welcome", 3*time.Second)
	time.Sleep(80 * time.Millisecond)
	if _, err := ptmx.Write([]byte("\x1b[B\x1b[B\r\x1b[B\r\x1b")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(cfg)
		if err == nil && strings.Contains(string(body), "frame") {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	body, _ := os.ReadFile(cfg)
	mu.Lock()
	plain := ansiRe.ReplaceAllString(buf.String(), "")
	mu.Unlock()
	t.Fatalf("welcome not updated\n%s\n%s", body, plain)
}

func TestSetDefaultThemeNoClear(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == "set-default-theme" {
		setDefaultChild()
		return
	}
	raw, cfg := driveSetDefault(t, "TestSetDefaultThemeNoClear", "set-default-theme", "defaults:\n  ui:\n    theme: jade\n", "/set default theme\r")
	assertNoScreenClear(t, raw)
	sc := newScreen(48, 120)
	sc.feed(raw)
	if n := blockLines(sc); n < 1 || n > 5 {
		t.Fatalf("wordmark rows %d\n%s", n, sc.dump())
	}
	if !strings.Contains(sc.dump(), "main-model") {
		t.Fatalf("welcome left the screen:\n%s", sc.dump())
	}
	f, err := config.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if c.PaletteName() != config.Builtin().PaletteName() {
		t.Fatalf("theme %s", c.PaletteName())
	}
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "jade") {
		t.Fatalf("override kept\n%s", body)
	}
}

func TestSetDefaultAllRestores(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == "set-default-all" {
		setDefaultChild()
		return
	}
	raw, cfg := driveSetDefault(t, "TestSetDefaultAllRestores", "set-default-all", "defaults:\n  ui:\n    theme: jade\n  agent:\n    max_steps:\n      act: 3\n", "/set default all\ry")
	assertNoScreenClear(t, raw)
	plain := ansiRe.ReplaceAllString(string(raw), "")
	if !strings.Contains(plain, "will be reset") {
		t.Fatalf("prompt missing\n%s", plain)
	}
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != config.InitYAML() {
		t.Fatal("file is not the factory document")
	}
	f, err := config.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if c.PaletteName() != "amber" || c.Agent.MaxSteps.Act != config.Builtin().Agent.MaxSteps.Act {
		t.Fatalf("theme %s steps %d", c.PaletteName(), c.Agent.MaxSteps.Act)
	}
}

func assertNoScreenClear(t *testing.T, raw []byte) {
	t.Helper()
	// Startup replaces the splash with one home-clear. A theme or /set
	// change must not clear again, and must not wipe scrollback.
	if n := bytes.Count(raw, []byte("\x1b[H\x1b[2J")); n != 1 || bytes.Contains(raw, []byte("\x1b[3J")) {
		t.Fatalf("cleared the screen (%d home-clears):\n%s", n, ansiRe.ReplaceAll(raw, nil))
	}
}

func setDefaultChild() {
	dir := os.Getenv("KONSOLAI_THEME_CWD")
	cfgPath := os.Getenv("KONSOLAI_THEME_CFG")
	prof := config.Builtin()
	if f, err := config.Load(cfgPath); err == nil {
		if c, err := f.Resolve(""); err == nil {
			prof = c
		}
	}
	prof.SetMain("http://127.0.0.1:1/v1", "main-model", false)
	prof.Roles.Fast = ""
	prof.UI.ClearOnStart = false
	prof.UI.Banner = true
	if prof.WelcomeLayout() == "" {
		prof.UI.Welcome = "split"
	}
	env := &commands.Env{
		Profile:    prof,
		Out:        os.Stdout,
		Err:        os.Stderr,
		Cwd:        dir,
		Version:    "vtest",
		ConfigPath: cfgPath,
		InRepl:     true,
	}
	code := Run(context.Background(), Config{
		Env: env, Reg: commands.Default(), Version: "vtest", Model: "main-model",
		Out: os.Stdout, Err: os.Stderr,
	})
	os.Exit(code)
}

func driveSetDefault(t *testing.T, testName, child, cfgBody, keys string) ([]byte, string) {
	t.Helper()
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
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
	if err := os.WriteFile(cfg, []byte(cfgBody), 0600); err != nil {
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
	waitPlain := func(needle string, d time.Duration) {
		t.Helper()
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			mu.Lock()
			plain := ansiRe.ReplaceAllString(strings.ReplaceAll(buf.String(), "\r", ""), "")
			mu.Unlock()
			if strings.Contains(plain, needle) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		mu.Lock()
		plain := ansiRe.ReplaceAllString(strings.ReplaceAll(buf.String(), "\r", ""), "")
		mu.Unlock()
		t.Fatalf("timeout waiting for %q\n%s", needle, plain)
	}
	waitPlain("main-model", 4*time.Second)
	time.Sleep(150 * time.Millisecond)
	if _, err := ptmx.Write([]byte(keys)); err != nil {
		t.Fatal(err)
	}
	waitPlain("applied now", 3*time.Second)
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	out := append([]byte(nil), buf.Bytes()...)
	mu.Unlock()
	return out, cfg
}

func setMenuChild() {
	dir := os.Getenv("KONSOLAI_THEME_CWD")
	cfgPath := os.Getenv("KONSOLAI_THEME_CFG")
	prof := config.Builtin()
	prof.SetMain("http://127.0.0.1:1/v1", "main-model", false)
	prof.Roles.Fast = ""
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
