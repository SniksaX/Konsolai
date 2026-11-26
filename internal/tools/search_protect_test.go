package tools

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/safety"
)

func searchProtectFixture(t *testing.T) (project string, reg *Registry) {
	t.Helper()
	root := t.TempDir()
	project = filepath.Join(root, "project")
	home := filepath.Join(project, "home")
	if err := os.MkdirAll(filepath.Join(project, ".real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "konsolai"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	active := filepath.Join(project, ".real", "config.yaml")
	body := "defaults:\n  database:\n    url: postgres://u:p@db.internal.example:5432/k\n  mcp:\n    servers:\n      s:\n        command: server-binary\n        args: [SECRETARG]\n        env:\n          TOKEN: SECRETENV\n        url: https://mcp.internal.example/rpc\n"
	if err := os.WriteFile(active, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KONSOLAI_CONFIG", active)
	// Evidence: the config is symlinked into the project root.
	if err := os.Symlink(active, filepath.Join(project, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "konsolai", "secrets"), []byte("main=SECRETKEYVALUE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "notes.txt"), []byte("visible project text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := safety.New(project)
	if err != nil {
		t.Fatal(err)
	}
	lister := func(context.Context) ([]string, error) {
		var out []string
		err := filepath.WalkDir(project, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, rerr := filepath.Rel(project, path)
			if rerr == nil {
				out = append(out, filepath.ToSlash(rel))
			}
			return nil
		})
		return out, err
	}
	return project, NewRegistry(g, io.Discard, lister, nil, nil)
}

func runSearch(t *testing.T, reg *Registry, args string) string {
	t.Helper()
	tool, ok := reg.Get("search")
	if !ok {
		t.Fatal("search tool missing")
	}
	act, err := tool.Prepare(args)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return res.Output
}

func TestSearchDoesNotLeakTheProtectedConfig(t *testing.T) {
	_, reg := searchProtectFixture(t)
	out := runSearch(t, reg, `{"pattern":"SECRETARG"}`)
	if strings.Contains(out, "SECRETARG") {
		t.Fatalf("search leaked the config argument:\n%s", out)
	}
	raw, _ := json.Marshal(out)
	for _, leak := range []string{"SECRETENV", "SECRETHEADER", "server-binary", "db.internal.example", "mcp.internal.example", "postgres://u:p@"} {
		if strings.Contains(string(raw), leak) || strings.Contains(out, leak) {
			t.Fatalf("search leaked %q:\n%s", leak, out)
		}
	}
}

func TestSearchOfSecretsIsSkippedWithMarker(t *testing.T) {
	_, reg := searchProtectFixture(t)
	out := runSearch(t, reg, `{"pattern":"SECRETKEYVALUE","path":"home/.config/konsolai"}`)
	if strings.Contains(out, "SECRETKEYVALUE") {
		t.Fatalf("search leaked the secrets file:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "skipped") {
		t.Fatalf("secrets search has no clear skip marker:\n%s", out)
	}
}

func TestSearchOfNormalProjectFilesStillWorks(t *testing.T) {
	_, reg := searchProtectFixture(t)
	out := runSearch(t, reg, `{"pattern":"visible project"}`)
	if !strings.Contains(out, "notes.txt") || !strings.Contains(out, "visible project text") {
		t.Fatalf("ordinary search broken:\n%s", out)
	}
}
