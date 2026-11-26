package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func protectedFixture(t *testing.T) (project, active, home string) {
	t.Helper()
	root := t.TempDir()
	project = filepath.Join(root, "project")
	home = filepath.Join(root, "home")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "konsolai"), 0o700); err != nil {
		t.Fatal(err)
	}
	active = filepath.Join(home, ".config", "konsolai", "config.yaml")
	body := "defaults:\n" +
		"  database:\n    url: postgres://user:pass@db.internal.example:5432/konsolai\n" +
		"  mcp:\n    servers:\n      remote:\n        transport: http\n        url: https://mcp.internal.example/rpc\n        command: server-binary\n        args: ['--token', 'SECRETARG']\n        env:\n          TOKEN: SECRETENV\n        headers:\n          Authorization: SECRETHEADER\n"
	if err := os.WriteFile(active, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("KONSOLAI_CONFIG", active)
	// The evidence scenario: the config is symlinked into the project root.
	if err := os.Symlink(active, filepath.Join(project, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	return project, active, home
}

func TestProtectedPathsFollowSymlinkBackupsAndSecrets(t *testing.T) {
	project, active, home := protectedFixture(t)
	backup := active + ".bak-1"
	if err := os.WriteFile(backup, []byte("old: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secrets := filepath.Join(home, ".config", "konsolai", "secrets")
	if err := os.WriteFile(secrets, []byte("m=SECRETKEY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		filepath.Join(project, "config.yaml"): ProtectedConfig,
		active:                                ProtectedConfig,
		backup:                                ProtectedBackup,
		secrets:                               ProtectedSecrets,
	} {
		kind, ok := ProtectedKindOf(path)
		if !ok || kind != want {
			t.Fatalf("%s = %q,%v want %q", path, kind, ok, want)
		}
		if !IsProtectedWritePath(path) {
			t.Fatalf("%s not write-protected", path)
		}
	}
	if kind, ok := ProtectedKindOf(filepath.Join(project, "normal.go")); ok {
		t.Fatalf("normal file classified as %q", kind)
	}
}

func TestMaskConfigContentHidesCommandsAndHosts(t *testing.T) {
	_, active, _ := protectedFixture(t)
	raw, err := os.ReadFile(active)
	if err != nil {
		t.Fatal(err)
	}
	masked := MaskConfigContent(string(raw))
	for _, leak := range []string{"server-binary", "SECRETARG", "SECRETENV", "SECRETHEADER", "db.internal.example", "mcp.internal.example"} {
		if strings.Contains(masked, leak) {
			t.Fatalf("masked config leaks %q:\n%s", leak, masked)
		}
	}
	for _, want := range []string{"<set>", "<masked>"} {
		if !strings.Contains(masked, want) {
			t.Fatalf("masked config misses %q:\n%s", want, masked)
		}
	}
	if got := MaskConfigContent("not: [valid"); !strings.Contains(got, "masked") {
		t.Fatalf("invalid YAML must fall back to a marker, got %q", got)
	}
}

func TestShellProtectedNames(t *testing.T) {
	_, _, _ = protectedFixture(t)
	for _, cmd := range []string{
		"echo hi > config.yaml", "cat config.yaml", "tee -a config.yaml",
		"sed -i s/a/b/ config.yaml", "mv x config.yaml.bak-1", "cp x config.yaml",
		"truncate -s 0 config.yaml", "dd of=config.yaml", "python3 -c 'open(\"config.yaml\",\"w\")'",
		"KONSOLAI_CONFIG=/tmp/x go run .",
	} {
		if _, ok := ShellProtected(cmd); !ok {
			t.Fatalf("shell command not protected: %s", cmd)
		}
	}
	if _, ok := ShellProtected("go test ./..."); ok {
		t.Fatal("ordinary command flagged")
	}
}
