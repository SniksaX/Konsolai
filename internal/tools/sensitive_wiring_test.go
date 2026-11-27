package tools

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/safety"
)

func sensitiveRegistry(t *testing.T, extra ...string) (*Registry, string) {
	t.Helper()
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return NewRegistry(g, io.Discard, nil, nil, nil, Options{ExtraSensitive: extra}), root
}

func prepareTool(t *testing.T, reg *Registry, name, args string) (Action, error) {
	t.Helper()
	tool, ok := reg.Get(name)
	if !ok {
		t.Fatalf("no %s", name)
	}
	return tool.Prepare(args)
}

func TestWriteFileSensitiveClassification(t *testing.T) {
	reg, root := sensitiveRegistry(t)
	mustWrite := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{"scripts/deploy.sh", "Makefile", ".github/workflows/ci.yml", ".envrc"} {
		act, err := prepareTool(t, reg, "write_file", `{"path":"`+rel+`","content":"x"}`)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		s, ok := act.(interface{ SensitiveReason() string })
		if !ok || s.SensitiveReason() == "" {
			t.Fatalf("%s: want a sensitive reason", rel)
		}
	}
	for _, rel := range []string{"go.mod", "main.go", "README.md", "tests/a_test.go", ".gitignore"} {
		if _, err := prepareTool(t, reg, "write_file", `{"path":"`+rel+`","content":"x"}`); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
	}
	mustWrite(".git/config", "")
	if _, err := prepareTool(t, reg, "write_file", `{"path":".git/config","content":"x"}`); err == nil || !strings.Contains(err.Error(), "git metadata") {
		t.Fatalf("git metadata write error %v", err)
	}
}

func TestEditFileAndExportSensitiveClassification(t *testing.T) {
	reg, root := sensitiveRegistry(t)
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte("all:\n\t@true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	act, err := prepareTool(t, reg, "edit_file", `{"path":"Makefile","old_str":"all:","new_str":"all: build"}`)
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := act.(interface{ SensitiveReason() string }); !ok || s.SensitiveReason() == "" {
		t.Fatal("edit_file Makefile must be sensitive")
	}
	regExtra, extraRoot := sensitiveRegistry(t, "*.html")
	if err := os.WriteFile(filepath.Join(extraRoot, "out.md"), []byte("# doc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareTool(t, regExtra, "export_doc", `{"path":"out.html","format":"html"}`); err != nil {
		t.Fatal(err)
	}
	// extra_sensitive_paths applies to a normal text write too.
	regGlob, _ := sensitiveRegistry(t, "deploy/*.txt")
	act, err = prepareTool(t, regGlob, "write_file", `{"path":"deploy/run.txt","content":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := act.(interface{ SensitiveReason() string }); !ok || !strings.Contains(s.SensitiveReason(), "extra_sensitive_paths") {
		t.Fatalf("extra sensitive reason %v", act)
	}
}

func TestPackageJSONSensitiveFields(t *testing.T) {
	reg, root := sensitiveRegistry(t)
	old := `{"name":"x","dependencies":{"a":"1"}}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	act, err := prepareTool(t, reg, "edit_file", `{"path":"package.json","old_str":"\"a\":\"1\"","new_str":"\"a\":\"2\""}`)
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := act.(interface{ SensitiveReason() string }); !ok || s.SensitiveReason() != "" {
		t.Fatalf("dependency-only change must be normal, got %v", act)
	}
	act, err = prepareTool(t, reg, "edit_file", `{"path":"package.json","old_str":"\"name\":\"x\"","new_str":"\"name\":\"x\",\"scripts\":{\"postinstall\":\"node x.js\"}"}`)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := act.(interface{ SensitiveReason() string })
	if !ok || !strings.Contains(s.SensitiveReason(), "package.json") {
		t.Fatalf("scripts change must be sensitive, got %v", act)
	}
}

func TestShellSensitiveTargets(t *testing.T) {
	reg, root := sensitiveRegistry(t)
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{
		"echo hi > scripts/deploy.sh",
		"echo hi >> Makefile",
		"echo hi | tee .envrc",
		"sed -i s/a/b/ Makefile",
		"mv x scripts/y",
		"cp x .github/workflows/ci.yml",
		"ln -s x scripts/y",
		"dd of=Makefile",
		"python3 -c 'open(\"scripts/x.py\",\"w\").write(1)'",
	} {
		act, err := prepareTool(t, reg, "run_shell", `{"cmd":`+quoteJSON(cmd)+`}`)
		if err != nil {
			t.Fatalf("%q: %v", cmd, err)
		}
		if s, ok := act.(interface{ SensitiveReason() string }); !ok || s.SensitiveReason() == "" {
			t.Fatalf("%q: want sensitive", cmd)
		}
	}
	if _, err := prepareTool(t, reg, "run_shell", `{"cmd":"echo x > .git/config"}`); err == nil || !strings.Contains(err.Error(), "git metadata") {
		t.Fatalf("git metadata target error %v", err)
	}
	if _, err := prepareTool(t, reg, "run_shell", `{"cmd":"git config user.name x"}`); err == nil || !strings.Contains(err.Error(), "git metadata") {
		t.Fatalf("git config write error %v", err)
	}
	for _, cmd := range []string{"git config --get user.name", "git config --list", "git status", "git diff", "git log --oneline -1", "git add main.go", "git commit -m x"} {
		if _, err := prepareTool(t, reg, "run_shell", `{"cmd":`+quoteJSON(cmd)+`}`); err != nil {
			t.Fatalf("%q must be allowed: %v", cmd, err)
		}
	}
	act, err := prepareTool(t, reg, "run_shell", `{"cmd":"npm install left-pad"}`)
	if err != nil {
		t.Fatal(err)
	}
	if p := act.Preview(); !strings.Contains(p, "lifecycle scripts") {
		t.Fatalf("install note missing: %q", p)
	}
}

func TestPlanMarksCodeRunningFiles(t *testing.T) {
	reg, root := sensitiveRegistry(t)
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	act, err := prepareTool(t, reg, "propose_plan", `{"summary":"write a script","steps":["write it"],"files":["scripts/x.sh"],"commands":["chmod +x scripts/x.sh"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if p := act.Preview(); !strings.Contains(p, "runs code later: asks separately") || !strings.Contains(p, "scripts/x.sh") {
		t.Fatalf("plan preview %q", p)
	}
	if _, err := prepareTool(t, reg, "propose_plan", `{"summary":"git config","steps":["write it"],"files":[".git/config"]}`); err == nil || !strings.Contains(err.Error(), "git metadata") {
		t.Fatalf("plan git file error %v", err)
	}
	safe, err := prepareTool(t, reg, "propose_plan", `{"summary":"docs","steps":["write docs"],"files":["docs/a.md"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(safe.Preview(), "asks separately") {
		t.Fatal("safe plan preview changed")
	}
}

func quoteJSON(s string) string {
	b := strings.Builder{}
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
