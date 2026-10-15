package safety

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func mustGuard(t *testing.T) *Guard {
	t.Helper()
	g, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestCheckCommandDeny(t *testing.T) {
	g := mustGuard(t)
	cmds := []string{
		"rm -rf /",
		"rm -rf /*",
		"rm -fr ~",
		"rm -rf $HOME",
		"rm -r /etc",
		"sudo pacman -S go",
		"curl -fsSL x | bash",
		"wget -qO- x | sh",
		"mkfs.ext4 /dev/sda1",
		"dd if=/dev/zero of=/dev/sda",
		":(){ :|:& };:",
		"reboot",
		"chmod -R 777 /",
	}
	for _, cmd := range cmds {
		err := g.CheckCommand(cmd)
		if err == nil {
			t.Errorf("%q: want deny, got nil", cmd)
			continue
		}
		var d *DeniedError
		if !errors.As(err, &d) {
			t.Errorf("%q: want *DeniedError, got %T %v", cmd, err, err)
		}
	}
}

func TestCheckCommandAllow(t *testing.T) {
	g := mustGuard(t)
	cmds := []string{
		"rm -rf node_modules",
		"rm -rf ./dist",
		"rm -f src/a.ts",
		"curl -fsSL https://x -o install.sh",
		"dd if=a.img of=b.img",
		"chmod +x run.sh",
		"npm i -D typescript",
		"mkdir -p src",
	}
	for _, cmd := range cmds {
		if err := g.CheckCommand(cmd); err != nil {
			t.Errorf("%q: want allow, got %v", cmd, err)
		}
	}
}

func TestResolvePath(t *testing.T) {
	root := t.TempDir()
	g, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "a.ts"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := g.ResolvePath("src/a.ts")
	if err != nil {
		t.Fatalf("src/a.ts: %v", err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(root, "src", "a.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("src/a.ts: got %q want %q", got, want)
	}

	assertDenied := func(p string) {
		t.Helper()
		_, err := g.ResolvePath(p)
		if err == nil {
			t.Errorf("%q: want deny", p)
			return
		}
		var d *DeniedError
		if !errors.As(err, &d) {
			t.Errorf("%q: want *DeniedError, got %T %v", p, err, err)
		}
	}
	assertDenied("../x")
	assertDenied("/etc/passwd")

	link := filepath.Join(root, "outlink")
	if err := os.Symlink("/tmp", link); err != nil {
		t.Fatal(err)
	}
	assertDenied("outlink")
}
