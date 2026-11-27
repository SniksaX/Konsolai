package safety

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClassifyWriteGitMetadata(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, ".git", "config"), "[core]\n")
	writeTestFile(t, filepath.Join(root, ".git", "hooks", "pre-commit"), "#!/bin/sh\n")
	writeTestFile(t, filepath.Join(root, ".git", "info", "exclude"), "*.tmp\n")
	for _, rel := range []string{
		".git/config", ".git/hooks/pre-commit", ".git/info/exclude",
		filepath.Join("sub", "..", ".git", "config"),
	} {
		class, reason := ClassifyWriteAt(root, filepath.Join(root, rel), nil)
		if class != WriteNever {
			t.Fatalf("%s class %v reason %q", rel, class, reason)
		}
	}
	// A symlink into .git resolves to the metadata directory.
	link := filepath.Join(root, "gitlink")
	if err := os.Symlink(filepath.Join(root, ".git", "config"), link); err != nil {
		t.Skip("symlink:", err)
	}
	if class, _ := ClassifyWriteAt(root, link, nil); class != WriteNever {
		t.Fatalf("symlink into .git class %v", class)
	}
}

func TestClassifyWriteGitCaseVariant(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, ".GIT", "config"), "[core]\n")
	class, reason := ClassifyWriteAt(root, filepath.Join(root, ".GIT", "config"), nil)
	if class != WriteNever {
		t.Fatalf("case variant class %v reason %q", class, reason)
	}
}

func TestClassifyWriteGitFilePointerAndCommonDir(t *testing.T) {
	root := t.TempDir()
	common := filepath.Join(root, ".bare")
	worktreeGit := filepath.Join(common, "worktrees", "wt")
	if err := os.MkdirAll(worktreeGit, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(worktreeGit, "commondir"), "../..\n")
	writeTestFile(t, filepath.Join(root, ".git"), "gitdir: "+filepath.Join(".bare", "worktrees", "wt")+"\n")
	writeTestFile(t, filepath.Join(common, "config"), "[core]\n")
	writeTestFile(t, filepath.Join(worktreeGit, "HEAD"), "ref: refs/heads/main\n")

	if class, _ := ClassifyWriteAt(root, filepath.Join(root, ".git"), nil); class != WriteNever {
		t.Fatalf(".git pointer class %v", class)
	}
	if class, _ := ClassifyWriteAt(root, filepath.Join(common, "config"), nil); class != WriteNever {
		t.Fatalf("common dir class %v", class)
	}
	if class, _ := ClassifyWriteAt(root, filepath.Join(worktreeGit, "HEAD"), nil); class != WriteNever {
		t.Fatalf("worktree gitdir class %v", class)
	}
	// A normal worktree file is not metadata.
	if class, _ := ClassifyWriteAt(root, filepath.Join(root, "main.go"), nil); class == WriteNever {
		t.Fatal("worktree source classified as git metadata")
	}
}

func TestClassifyWriteCodeRunning(t *testing.T) {
	root := t.TempDir()
	confirm := []string{
		"Makefile", "makefile", "GNUmakefile", "justfile", "Taskfile.yml",
		".pre-commit-config.yaml", "lefthook.yml", ".envrc", ".gitlab-ci.yml",
		".npmrc", "go.work", "setup.py", "setup.cfg", "pyproject.toml",
		"tox.ini", "noxfile.py", "conftest.py", "build.rs", ".pnpmfile.cjs",
		"Dockerfile", "Dockerfile.dev", "docker-compose.yml", "docker-compose.prod.yaml",
		".yarnrc", ".yarnrc.yml", ".cargo/config.toml",
		".husky/pre-commit", ".github/workflows/ci.yml", ".circleci/config.yml",
		".vscode/tasks.json", ".vscode/settings.json", ".devcontainer/devcontainer.json",
		"scripts/build.sh",
	}
	for _, rel := range confirm {
		path := filepath.Join(root, filepath.FromSlash(rel))
		writeTestFile(t, path, "x\n")
		class, reason := ClassifyWriteAt(root, path, nil)
		if class != WriteConfirm {
			t.Fatalf("%s class %v reason %q", rel, class, reason)
		}
		if !strings.Contains(reason, "runs code later") {
			t.Fatalf("%s reason %q", rel, reason)
		}
	}
	safe := []string{"go.mod", "main.go", "README.md", "main_test.go", ".gitignore", "package-lock.json", "src/index.js", "sub/scripts/build.sh"}
	for _, rel := range safe {
		path := filepath.Join(root, filepath.FromSlash(rel))
		writeTestFile(t, path, "x\n")
		if class, reason := ClassifyWriteAt(root, path, nil); class != WriteSafe {
			t.Fatalf("%s class %v reason %q", rel, class, reason)
		}
	}
}

func TestClassifyWriteNewFileUnderGlob(t *testing.T) {
	root := t.TempDir()
	if class, _ := ClassifyWriteAt(root, filepath.Join(root, ".github", "workflows", "new.yml"), nil); class != WriteConfirm {
		t.Fatalf("new workflow class %v", class)
	}
	if class, _ := ClassifyWriteAt(root, filepath.Join(root, "scripts", "new.sh"), nil); class != WriteConfirm {
		t.Fatalf("new root script class %v", class)
	}
}

func TestClassifyWriteInfersGitRootForScripts(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := writeTestFile(t, filepath.Join(root, "scripts", "build.sh"), "x\n")
	if class, reason := ClassifyWrite(script, nil); class != WriteConfirm {
		t.Fatalf("inferred root script class %v reason %q", class, reason)
	}
	if class, _ := ClassifyWrite(filepath.Join(root, "README.md"), nil); class != WriteSafe {
		t.Fatalf("inferred root readme class %v", class)
	}
}

func TestClassifyWriteExtraPatternsAndCase(t *testing.T) {
	root := t.TempDir()
	path := writeTestFile(t, filepath.Join(root, "infra", "deploy.yaml"), "x\n")
	if class, reason := ClassifyWriteAt(root, path, []string{"infra/**"}); class != WriteConfirm || !strings.Contains(reason, "extra_sensitive_paths") {
		t.Fatalf("extra glob class %v reason %q", class, reason)
	}
	if class, _ := ClassifyWriteAt(root, path, []string{"INFRA/**"}); class != WriteConfirm {
		t.Fatalf("case-insensitive extra glob class %v", class)
	}
	if class, _ := ClassifyWriteAt(root, path, []string{"*.yaml"}); class != WriteConfirm {
		t.Fatalf("basename glob class %v", class)
	}
	if class, _ := ClassifyWriteAt(root, path, []string{"other/**"}); class != WriteSafe {
		t.Fatalf("non-matching glob class %v", class)
	}
	upper := filepath.Join(root, ".github", "workflows", "ci.yml")
	writeTestFile(t, upper, "x\n")
	if class, _ := ClassifyWriteAt(root, upper, nil); class != WriteConfirm {
		t.Fatalf("glob case class %v", class)
	}
}

func TestClassifyWriteSymlinkedCodeRunningFile(t *testing.T) {
	root := t.TempDir()
	makefile := writeTestFile(t, filepath.Join(root, "Makefile"), "all:\n")
	link := filepath.Join(root, "build")
	if err := os.Symlink(makefile, link); err != nil {
		t.Skip("symlink:", err)
	}
	if class, _ := ClassifyWriteAt(root, link, nil); class != WriteConfirm {
		t.Fatalf("symlinked Makefile class %v", class)
	}
}

func TestClassifyWriteReasonHidesSecrets(t *testing.T) {
	root := t.TempDir()
	secret := "sk-" + strings.Repeat("a", 24)
	path := writeTestFile(t, filepath.Join(root, secret, "run.sh"), "x\n")
	_, reason := ClassifyWriteAt(root, path, []string{"**/" + secret + "/**"})
	if strings.Contains(reason, secret) {
		t.Fatalf("reason leaks the secret: %q", reason)
	}
}

func TestPackageJSONChange(t *testing.T) {
	base := `{"name":"app","version":"1.0.0","dependencies":{"react":"^18.0.0"},"scripts":{"build":"tsc"},"main":"dist/index.js","type":"commonjs","bin":{"app":"bin/app.js"}}`
	depsOnly := `{"name":"app","version":"1.0.1","dependencies":{"react":"^19.0.0"},"scripts":{"build":"tsc"},"main":"dist/index.js","type":"commonjs","bin":{"app":"bin/app.js"}}`
	if sensitive, diff, err := PackageJSONChange(base, depsOnly); err != nil || sensitive || diff != "" {
		t.Fatalf("dependency-only %v %q %v", sensitive, diff, err)
	}
	scriptChanged := `{"name":"app","dependencies":{"react":"^18.0.0"},"scripts":{"build":"vite build"},"main":"dist/index.js","type":"commonjs","bin":{"app":"bin/app.js"}}`
	sensitive, diff, err := PackageJSONChange(base, scriptChanged)
	if err != nil || !sensitive || !strings.Contains(diff, "scripts:") || !strings.Contains(diff, "build") {
		t.Fatalf("script change %v %q %v", sensitive, diff, err)
	}
	lifecycle := `{"name":"app","dependencies":{"react":"^18.0.0"},"scripts":{"build":"tsc","preinstall":"node setup.js"},"main":"dist/index.js","type":"commonjs","bin":{"app":"bin/app.js"}}`
	if sensitive, diff, err = PackageJSONChange(base, lifecycle); err != nil || !sensitive || !strings.Contains(diff, "preinstall") {
		t.Fatalf("lifecycle change %v %q %v", sensitive, diff, err)
	}
	for name, changed := range map[string]string{
		"bin":  `{"name":"app","dependencies":{"react":"^18.0.0"},"scripts":{"build":"tsc"},"main":"dist/index.js","type":"commonjs","bin":{"app":"bin/other.js"}}`,
		"type": `{"name":"app","dependencies":{"react":"^18.0.0"},"scripts":{"build":"tsc"},"main":"dist/index.js","type":"module","bin":{"app":"bin/app.js"}}`,
		"main": `{"name":"app","dependencies":{"react":"^18.0.0"},"scripts":{"build":"tsc"},"main":"dist/main.js","type":"commonjs","bin":{"app":"bin/app.js"}}`,
	} {
		sensitive, diff, err = PackageJSONChange(base, changed)
		if err != nil || !sensitive || !strings.Contains(diff, name+":") {
			t.Fatalf("%s change %v %q %v", name, sensitive, diff, err)
		}
	}
	if sensitive, _, err = PackageJSONChange(base, base); err != nil || sensitive {
		t.Fatalf("same %v %v", sensitive, err)
	}
	if _, _, err = PackageJSONChange(base, "{not json"); err == nil {
		t.Fatal("invalid JSON did not error")
	}
}

func TestPackageJSONChangeHidesSecrets(t *testing.T) {
	secret := "sk-" + strings.Repeat("b", 24)
	old := `{"scripts":{"build":"tsc"}}`
	new := `{"scripts":{"build":"deploy --token ` + secret + `"}}`
	sensitive, diff, err := PackageJSONChange(old, new)
	if err != nil || !sensitive {
		t.Fatalf("%v %q %v", sensitive, diff, err)
	}
	if strings.Contains(diff, secret) {
		t.Fatalf("diff leaks the secret: %q", diff)
	}
	if !strings.Contains(diff, "[REDACTED") {
		t.Fatalf("diff did not mask: %q", diff)
	}
}

func TestInstallNote(t *testing.T) {
	positives := map[string]string{
		"npm install":                     "lifecycle",
		"npm i":                           "lifecycle",
		"npm add left-pad":                "lifecycle",
		"pnpm install":                    "lifecycle",
		"yarn add react":                  "lifecycle",
		"python -m pip install requests":  "third-party",
		"pip install -r req.txt":          "third-party",
		"go install example.com/x@latest": "upstream",
		"cargo install ripgrep":           "upstream",
	}
	for cmd, want := range positives {
		if got := InstallNote(cmd); !strings.Contains(got, want) || strings.Contains(got, "\n") {
			t.Fatalf("%q -> %q (want %q)", cmd, got, want)
		}
	}
	for _, cmd := range []string{"npm run build", "npm test", "pip list", "go test ./...", "cargo build", "echo install", ""} {
		if got := InstallNote(cmd); got != "" {
			t.Fatalf("%q -> %q", cmd, got)
		}
	}
}

func TestResolvePhysical(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "real")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skip("symlink:", err)
	}
	got := ResolvePhysical(filepath.Join(link, "..", "real", "new.txt"))
	want := filepath.Join(dir, "new.txt")
	if got != want {
		t.Fatalf("ResolvePhysical %q want %q", got, want)
	}
}
