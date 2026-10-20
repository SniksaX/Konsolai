package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDockerArbitraryUser(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("needs docker")
	}
	if !dockerDaemon(t) {
		t.Skip("docker daemon is not available")
	}
	lockCompose(t)
	root := moduleRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if out, err := dockerCombined(ctx, "build", "--target", "base", "-t", "konsolai:dev", root); err != nil {
		t.Fatalf("build base: %v\n%s", err, out)
	}
	if out, err := dockerCombined(ctx, "build", "--target", "full", "-t", "konsolai:full", root); err != nil {
		t.Fatalf("build full: %v\n%s", err, out)
	}

	cfgDir := t.TempDir()
	if err := os.Chmod(cfgDir, 0777); err != nil {
		t.Fatal(err)
	}
	out, err := dockerCombined(ctx, "run", "--rm", "--user", "4242:4242", "--entrypoint", "bash",
		"-v", cfgDir+":/cfg", "konsolai:dev", "-c", "umask 077; printf '%s\n' 'defaults: {}' > /cfg/config.yaml")
	if err != nil {
		t.Fatalf("write config: %v\n%s", err, out)
	}
	cfg := filepath.Join(cfgDir, "config.yaml")
	fi, err := os.Stat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("config mode %o", fi.Mode().Perm())
	}
	if uid := fileUID(t, cfg); uid != 4242 {
		t.Fatalf("config uid %d", uid)
	}

	work := t.TempDir()
	if err := os.Chmod(work, 0777); err != nil {
		t.Fatal(err)
	}
	out, err = dockerCombined(ctx, "run", "--rm", "--user", "4242:4242",
		"-v", cfg+":/config/config.yaml:ro",
		"-v", work+":/work",
		"-w", "/work",
		"konsolai:dev", "/config", "path")
	if err != nil {
		t.Fatalf("config path: %v\n%s", err, out)
	}
	if strings.Contains(strings.ToLower(out), "permission denied") || !strings.Contains(out, "/config/config.yaml") {
		t.Fatalf("config path:\n%s", out)
	}

	out, err = dockerCombined(ctx, "run", "--rm", "--user", "4242:4242", "--entrypoint", "bash",
		"-v", work+":/work", "konsolai:dev", "-c", "printf '%s\n' owned > /work/owned.txt")
	if err != nil {
		t.Fatalf("write work file: %v\n%s", err, out)
	}
	if uid := fileUID(t, filepath.Join(work, "owned.txt")); uid != 4242 {
		t.Fatalf("owned.txt uid %d", uid)
	}

	state := filepath.Join(work, "state", "konsolai")
	if err := os.MkdirAll(state, 0755); err != nil {
		t.Fatal(err)
	}
	sess := []byte(`{"profile":"default","cwd":"/work","title":"Container note","updated_at":"2026-10-20T08:00:00Z","messages":[{"role":"user","content":"Write a note"},{"role":"assistant","content":"A short note for the PDF.\n"}]}`)
	if err := os.WriteFile(filepath.Join(state, "last_session.json"), sess, 0644); err != nil {
		t.Fatal(err)
	}
	out, err = dockerCombined(ctx, "run", "--rm", "--user", "4242:4242",
		"-v", cfg+":/config/config.yaml:ro",
		"-v", work+":/work",
		"-w", "/work",
		"-e", "XDG_STATE_HOME=/work/state",
		"konsolai:full", "/pdf", "note.pdf")
	if err != nil {
		t.Fatalf("pdf: %v\n%s", err, out)
	}
	low := strings.ToLower(out)
	if strings.Contains(low, "crashpad") || strings.Contains(low, "headless user data") || strings.Contains(low, "permission denied") {
		t.Fatalf("pdf output:\n%s", out)
	}
	pdf := filepath.Join(work, "note.pdf")
	b, err := os.ReadFile(pdf)
	if err != nil {
		t.Fatalf("read pdf: %v\n%s", err, out)
	}
	if !bytes.HasPrefix(b, []byte("%PDF")) {
		t.Fatalf("not a pdf (%d bytes): %q", len(b), b[:min(len(b), 24)])
	}
	if uid := fileUID(t, pdf); uid != 4242 {
		t.Fatalf("pdf uid %d", uid)
	}
}

func dockerCombined(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func fileUID(t *testing.T, path string) uint32 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("stat %T", fi.Sys())
	}
	return st.Uid
}
