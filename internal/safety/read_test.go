package safety

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveReadOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	f := filepath.Join(outside, "note.txt")
	if err := os.WriteFile(f, []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	g, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := g.ResolveRead(f)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(f)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if _, err := g.ResolvePath(f); err == nil {
		t.Fatal("write still confined")
	}
}

func TestSensitiveReadDenylist(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cases := []string{
		filepath.Join(home, ".ssh", "id_rsa"),
		filepath.Join(home, ".gnupg", "pubring.kbx"),
		filepath.Join(home, ".local", "share", "keyrings", "login.keyring"),
		filepath.Join(home, ".mozilla", "firefox", "prefs.js"),
		filepath.Join(home, ".config", "BraveSoftware", "x"),
		filepath.Join(home, ".config", "google-chrome", "x"),
		filepath.Join(home, ".config", "chromium", "x"),
		filepath.Join(home, ".config", "konsolai", "config.json"),
		"/proc/1/environ",
		filepath.Join(t.TempDir(), "secret.pem"),
		filepath.Join(t.TempDir(), "id_ed25519"),
		filepath.Join(t.TempDir(), ".netrc"),
	}
	for _, p := range cases {
		if !SensitiveRead(p) {
			t.Errorf("want sensitive %s", p)
		}
	}
	if SensitiveRead(filepath.Join(t.TempDir(), "readme.md")) {
		t.Fatal("readme.md is not sensitive")
	}
}

func TestResolveReadSensitiveFile(t *testing.T) {
	root := t.TempDir()
	g, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(root, "tls.pem")
	if err := os.WriteFile(key, []byte("k"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.ResolveRead(key); err != ErrSensitiveFile {
		t.Fatalf("pem: %v", err)
	}
	dir, err := g.ResolveReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if dir == "" {
		t.Fatal("list dir of folder with pem")
	}
}
