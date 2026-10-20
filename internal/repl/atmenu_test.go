package repl

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtQueryWordBoundary(t *testing.T) {
	head, query, tail, ok := atQuery("see @internal/ag now", len([]rune("see @internal/ag")))
	if !ok || head != "see " || query != "internal/ag" || tail != " now" {
		t.Fatalf("ok %v head %q query %q tail %q", ok, head, query, tail)
	}
	if _, _, _, ok := atQuery("email user@example.com", len([]rune("email user@example.com"))); ok {
		t.Fatal("email opened a file menu")
	}
	if _, _, _, ok := atQuery("plain", 5); ok {
		t.Fatal("plain text opened a file menu")
	}
}

func TestListAtFiltersDirsAndSensitive(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "internal"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "agent.go"), []byte("package agent\nfunc A() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data.bin"), []byte{0, 1, 2, 3}, 0644); err != nil {
		t.Fatal(err)
	}
	rows, capped := listAt(dir, "")
	if capped || len(rows) < 4 {
		t.Fatalf("capped %v rows %d", capped, len(rows))
	}
	if !rows[0].dir || rows[0].rel != "internal/" {
		t.Fatalf("first %q dir %v", rows[0].rel, rows[0].dir)
	}
	if !strings.HasPrefix(rows[0].right(), "dir · ") {
		t.Fatalf("dir meta %q", rows[0].right())
	}
	var sawEnv, sawBin, sawNotes bool
	for i := range rows {
		switch rows[i].rel {
		case ".env":
			sawEnv = true
			if rows[i].right() != "sensitive" {
				t.Fatalf("env meta %q", rows[i].right())
			}
			if strings.Contains(atDetail(&rows[i]), "SECRET") {
				t.Fatal("sensitive detail leaked")
			}
		case "data.bin":
			sawBin = true
			if rows[i].right() != "binary" {
				t.Fatalf("bin meta %q", rows[i].right())
			}
		case "notes.txt":
			sawNotes = true
			if !strings.Contains(rows[i].right(), "lines") {
				t.Fatalf("notes meta %q", rows[i].right())
			}
		}
	}
	if !sawEnv || !sawBin || !sawNotes {
		t.Fatalf("env %v bin %v notes %v", sawEnv, sawBin, sawNotes)
	}
	filt, _ := listAt(dir, "int")
	if len(filt) != 1 || filt[0].rel != "internal/" {
		t.Fatalf("filter %#v", filt)
	}
	inner, _ := listAt(dir, "internal/ag")
	if len(inner) != 1 || inner[0].rel != "internal/agent.go" {
		t.Fatalf("inner %#v", inner)
	}
}

func TestListAtGitignoreKeepsSensitive(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n.env\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keep.go"), []byte("package keep\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skip.log"), []byte("nope\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TOKEN=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, capped := listAt(dir, "")
	if capped {
		t.Fatal("capped")
	}
	for _, e := range rows {
		if e.rel == "skip.log" {
			t.Fatal("gitignored file listed")
		}
	}
	var keep, env bool
	for i := range rows {
		if rows[i].rel == "keep.go" {
			keep = true
		}
		if rows[i].rel == ".env" && rows[i].right() == "sensitive" {
			env = true
		}
	}
	if !keep || !env {
		t.Fatalf("keep %v env %v rows %v", keep, env, rels(rows))
	}
}

func TestListAtScanCap(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < atScanCap+1; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d", i)), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	rows, capped := listAt(dir, "")
	if !capped || rows != nil {
		t.Fatalf("capped %v rows %d", capped, len(rows))
	}
	m := newAtMenu(func() string { return dir })
	m.Sync("@", 1)
	if !m.Open() || !strings.Contains(m.Render(80), "refine the path") {
		t.Fatalf("render %q open %v", m.Render(80), m.Open())
	}
}

func TestAtMenuEscRestoresTyped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m := newAtMenu(func() string { return dir })
	m.Sync("@no", 3)
	if !m.Open() {
		t.Fatal("menu closed")
	}
	res := m.Down()
	if res.Line != "@notes.txt" {
		t.Fatalf("down %q", res.Line)
	}
	res = m.Esc()
	if res.Line != "@no" || m.Open() {
		t.Fatalf("esc line %q open %v", res.Line, m.Open())
	}
}

func rels(rows []atEntry) []string {
	out := make([]string, len(rows))
	for i, e := range rows {
		out[i] = e.rel
	}
	return out
}
