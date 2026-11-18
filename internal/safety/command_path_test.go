package safety

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCommandPathsOutside(t *testing.T) {
	g := mustGuard(t)
	err := g.CheckCommandPaths("rm -rf /tmp/x && mkdir /tmp/x", g.Base())
	var d *DeniedError
	if !errors.As(err, &d) || d.Rule != "escape" {
		t.Fatalf("bare command: %v", err)
	}
	if d.Detail != "/tmp/x" {
		t.Fatalf("detail %q", d.Detail)
	}
}

func TestCommandPathsRelativeToCwd(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	sib := filepath.Join(root, "sib")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sib, 0755); err != nil {
		t.Fatal(err)
	}
	g, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	subAbs, err := filepath.EvalSymlinks(sub)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.CheckCommandPaths("rm -rf ../sib", subAbs); err != nil {
		t.Fatalf("sibling via cwd: %v", err)
	}
	err = g.CheckCommandPaths("rm -rf ../../outside", subAbs)
	var d *DeniedError
	if !errors.As(err, &d) || d.Rule != "escape" {
		t.Fatalf("parent escape: %v", err)
	}
}

func TestCommandPathsCwdIsNotARoot(t *testing.T) {
	g := mustGuard(t)
	err := g.CheckCommandPaths("rm -rf /tmp/x && mkdir /tmp/x", "/tmp/x")
	var d *DeniedError
	if !errors.As(err, &d) || d.Rule != "escape" {
		t.Fatalf("cwd must not allow itself: %v", err)
	}
}

func TestCommandPathsReadOnlyOutside(t *testing.T) {
	g := mustGuard(t)
	if err := g.CheckCommandPaths("cat /etc/os-release", g.Base()); err != nil {
		t.Fatal(err)
	}
	if err := g.CheckCommandPaths("ls /tmp", g.Base()); err != nil {
		t.Fatal(err)
	}
}

func TestCommandPathsSymlink(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "out")
	if err := os.Symlink("/tmp", link); err != nil {
		t.Fatal(err)
	}
	g, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	err = g.CheckCommandPaths("rm -rf out", g.Base())
	var d *DeniedError
	if !errors.As(err, &d) || d.Rule != "symlink-escape" {
		t.Fatalf("symlink: %v", err)
	}
}

func TestCommandPathsScriptRedirect(t *testing.T) {
	g := mustGuard(t)
	if err := g.CheckCommandPaths("bash -c 'echo x > /etc/passwd'", g.Base()); err == nil {
		t.Fatal("a redirect outside the root must be denied")
	}
	if err := g.CheckCommandPaths("sh -c 'printf x > ~/escape'", g.Base()); err == nil {
		t.Fatal("a tilde redirect outside the root must be denied")
	}
	for _, cmd := range []string{
		"bash -c 'echo x > out.txt'",
		"bash -c 'echo x > /dev/null'",
		"bash -c 'echo x 2>&1'",
		"python3 -c 'print(1)'",
	} {
		if err := g.CheckCommandPaths(cmd, g.Base()); err != nil {
			t.Errorf("%q: %v", cmd, err)
		}
	}
}

func TestCommandPathsReadOnlySensitive(t *testing.T) {
	g := mustGuard(t)
	for _, cmd := range []string{
		"cat ~/.ssh/id_rsa",
		"head ~/.pgpass",
		"wc -l ~/.config/konsolai/config.yaml",
		"cat ~/.ssh/*",
		"cat ~/.gnupg/private-keys-v1.d",
	} {
		if err := g.CheckCommandPaths(cmd, g.Base()); !errors.Is(err, ErrSensitiveFile) {
			t.Errorf("%q: want ErrSensitiveFile, got %v", cmd, err)
		}
	}
	for _, cmd := range []string{
		"cat /etc/os-release",
		"ls /tmp",
		"grep -r id_rsa .",
		"cat README.md",
	} {
		if err := g.CheckCommandPaths(cmd, g.Base()); err != nil {
			t.Errorf("%q: %v", cmd, err)
		}
	}
}
