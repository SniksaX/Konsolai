package safety

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/SniksaX/Konsolai/internal/redact"
)

// WriteClass says how much care a write needs: Safe writes run no later code,
// Confirm writes can run code later, and Never writes touch git metadata.
type WriteClass int

const (
	WriteSafe WriteClass = iota
	WriteConfirm
	WriteNever
)

// String names the class.
func (c WriteClass) String() string {
	switch c {
	case WriteConfirm:
		return "confirm"
	case WriteNever:
		return "never"
	default:
		return "safe"
	}
}

// exactSensitiveNames are basenames that make or run code later.
var exactSensitiveNames = map[string]bool{
	"makefile":                true,
	"gnumakefile":             true,
	"justfile":                true,
	"taskfile.yml":            true,
	".pre-commit-config.yaml": true,
	"lefthook.yml":            true,
	".envrc":                  true,
	".gitlab-ci.yml":          true,
	".npmrc":                  true,
	"go.work":                 true,
	"setup.py":                true,
	"setup.cfg":               true,
	"pyproject.toml":          true,
	"tox.ini":                 true,
	"noxfile.py":              true,
	"conftest.py":             true,
	"build.rs":                true,
	".pnpmfile.cjs":           true,
}

// sensitivePathGlobs are project-relative path globs that run code later.
var sensitivePathGlobs = []string{
	".husky/**",
	".github/workflows/**",
	".circleci/**",
	".vscode/tasks.json",
	".vscode/settings.json",
	".devcontainer/**",
	".cargo/config.toml",
}

// ClassifyWrite classifies a resolved write path without a project root. The
// git worktree root is inferred when one exists, so scripts/ still classifies.
func ClassifyWrite(resolvedPath string, extraPatterns []string) (WriteClass, string) {
	gitDirs, workRoot := gitMetaFor(resolvedPath)
	class, reason := classifyWrite(workRoot, ResolvePhysical(resolvedPath), extraPatterns, gitDirs)
	return class, reason
}

// ClassifyWriteAt classifies a resolved write path relative to a project root.
// extraPatterns are project-relative globs that must confirm; ** matches
// across path separators and matching is case-insensitive.
func ClassifyWriteAt(root, resolvedPath string, extraPatterns []string) (WriteClass, string) {
	gitDirs, workRoot := gitMetaFor(resolvedPath)
	if strings.TrimSpace(root) == "" {
		root = workRoot
	} else {
		root = ResolvePhysical(root)
	}
	class, reason := classifyWrite(root, ResolvePhysical(resolvedPath), extraPatterns, gitDirs)
	return class, reason
}

func classifyWrite(root, abs string, extraPatterns, gitDirs []string) (WriteClass, string) {
	if abs == "" {
		return WriteSafe, ""
	}
	for _, dir := range gitDirs {
		if containsPathFold(dir, abs) {
			return WriteNever, "this file is git metadata (hooks, config and worktrees run code)"
		}
	}
	base := strings.ToLower(filepath.Base(abs))
	rel := ""
	if root != "" && containsPathFold(root, abs) {
		if r, err := filepath.Rel(root, abs); err == nil {
			rel = filepath.ToSlash(r)
		}
	}
	candidates := []string{}
	if rel != "" {
		candidates = append(candidates, rel)
	}
	candidates = append(candidates, filepath.ToSlash(abs))
	if reason := codeRunningReason(base, rel, candidates); reason != "" {
		return WriteConfirm, "this file runs code later: " + redact.String(reason)
	}
	for _, pattern := range extraPatterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if globMatchAny(pattern, candidates) {
			return WriteConfirm, "this file runs code later: matches extra_sensitive_paths " + redact.String(pattern)
		}
	}
	return WriteSafe, ""
}

// codeRunningReason returns the historical reason text for a code-running
// basename, path glob, or root scripts/ directory.
func codeRunningReason(base, rel string, candidates []string) string {
	if base != "" {
		if exactSensitiveNames[base] {
			return base
		}
		if strings.HasPrefix(base, "dockerfile") {
			return base
		}
		if strings.HasPrefix(base, "docker-compose") && (strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml")) {
			return base
		}
		if strings.HasPrefix(base, ".yarnrc") {
			return base
		}
	}
	if rel != "" {
		if r := strings.ToLower(rel); r == "scripts" || strings.HasPrefix(r, "scripts/") {
			return "scripts/ at the project root"
		}
	}
	for _, pattern := range sensitivePathGlobs {
		if globMatchAny(pattern, candidates) {
			return pattern
		}
	}
	return ""
}

// ResolvePhysical resolves symlinks in the deepest existing part of path and
// cleans any ".." segments. A missing final file keeps its name.
func ResolvePhysical(p string) string {
	if strings.TrimSpace(p) == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	resolved, err := evalDeepest(abs)
	if err != nil {
		return filepath.Clean(abs)
	}
	return resolved
}

// gitMetaFor finds the nearest .git directory or gitdir pointer above path and
// returns the physical git metadata dirs plus the worktree root.
func gitMetaFor(p string) ([]string, string) {
	abs := ResolvePhysical(p)
	if abs == "" {
		return nil, ""
	}
	cur := abs
	if fi, err := os.Lstat(cur); err != nil || !fi.IsDir() {
		cur = filepath.Dir(cur)
	}
	for {
		entry := findGitEntry(cur)
		if entry != "" {
			if fi, err := os.Lstat(entry); err == nil {
				if fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
					if dir, err := evalDeepest(entry); err == nil {
						return []string{dir}, cur
					}
				} else if fi.Mode().IsRegular() {
					if dir, ok := readGitdirPointer(entry); ok {
						dirs := []string{dir, filepath.Clean(entry)}
						if common, ok := readCommonDir(dir); ok {
							dirs = append(dirs, common)
						}
						return dirs, cur
					}
				}
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return nil, ""
		}
		cur = parent
	}
}

// findGitEntry returns "<dir>/.git" matching the name case-insensitively.
func findGitEntry(dir string) string {
	exact := filepath.Join(dir, ".git")
	if _, err := os.Lstat(exact); err == nil {
		return exact
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if strings.EqualFold(e.Name(), ".git") {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// readGitdirPointer parses a .git file ("gitdir: <path>") into a physical dir.
func readGitdirPointer(file string) (string, bool) {
	b, err := os.ReadFile(file)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 7 || !strings.EqualFold(line[:7], "gitdir:") {
			continue
		}
		target := strings.TrimSpace(line[7:])
		if target == "" {
			return "", false
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(file), target)
		}
		resolved, err := evalDeepest(target)
		if err != nil {
			return "", false
		}
		return resolved, true
	}
	return "", false
}

// readCommonDir reads a worktree gitdir's "commondir" pointer.
func readCommonDir(gitdir string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return "", false
	}
	target := strings.TrimSpace(string(b))
	if target == "" {
		return "", false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(gitdir, target)
	}
	resolved, err := evalDeepest(target)
	if err != nil {
		return "", false
	}
	return resolved, true
}

// containsPathFold is containsPath with case-insensitive components (for a
// case-insensitive volume) and a volume-aware clean.
func containsPathFold(root, p string) bool {
	if root == "" || p == "" {
		return false
	}
	r := strings.ToLower(filepath.ToSlash(filepath.Clean(root)))
	q := strings.ToLower(filepath.ToSlash(filepath.Clean(p)))
	if r == q {
		return true
	}
	return strings.HasPrefix(q, strings.TrimSuffix(r, "/")+"/")
}

// globMatchAny matches a glob against the relative and absolute candidates.
// A path-only pattern also matches at any depth.
func globMatchAny(pattern string, candidates []string) bool {
	patterns := []string{pattern}
	if !strings.HasPrefix(pattern, "/") && !strings.Contains(pattern, ":") {
		patterns = append(patterns, "**/"+pattern)
	}
	for _, pat := range patterns {
		re, err := globRegexp(pat)
		if err != nil {
			continue
		}
		for _, candidate := range candidates {
			if re.MatchString(filepath.ToSlash(candidate)) {
				return true
			}
		}
	}
	return false
}

// globRegexp compiles a case-insensitive glob with **, * and ?.
func globRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("(?i)^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
				}
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// PackageJSONChange compares two package.json documents and reports whether a
// change can run code later: scripts (including lifecycle hooks), bin, type or
// main. Dependency-only changes are not sensitive. The diff names only the
// changed sensitive fields, with secret-shaped values masked.
func PackageJSONChange(oldText, newText string) (bool, string, error) {
	oldObj, err := packageJSONObject(oldText)
	if err != nil {
		return false, "", err
	}
	newObj, err := packageJSONObject(newText)
	if err != nil {
		return false, "", err
	}
	var lines []string
	for _, field := range []string{"scripts", "bin", "type", "main"} {
		if jsonValueEqual(oldObj[field], newObj[field]) {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: %s -> %s", field, compactJSON(oldObj[field]), compactJSON(newObj[field])))
	}
	if len(lines) == 0 {
		return false, "", nil
	}
	return true, redact.String(strings.Join(lines, "\n")), nil
}

func packageJSONObject(text string) (map[string]json.RawMessage, error) {
	if strings.TrimSpace(text) == "" {
		return map[string]json.RawMessage{}, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &obj); err != nil {
		return nil, fmt.Errorf("safety: package.json: %w", err)
	}
	if obj == nil {
		obj = map[string]json.RawMessage{}
	}
	return obj, nil
}

func jsonValueEqual(a, b json.RawMessage) bool {
	av, aok := decodeJSONValue(a)
	bv, bok := decodeJSONValue(b)
	if !aok || !bok {
		return aok == bok
	}
	return reflect.DeepEqual(av, bv)
}

func decodeJSONValue(raw json.RawMessage) (interface{}, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, false
	}
	return v, true
}

func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "null"
	}
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return "null"
	}
	return b.String()
}

// InstallNote returns one terse line when cmd installs upstream code, else "".
func InstallNote(cmd string) string {
	fields := strings.Fields(cmd)
	for i, f := range fields {
		switch strings.ToLower(f) {
		case "npm", "pnpm", "yarn":
			verb, ok := nextVerb(fields, i+1)
			if !ok {
				continue
			}
			switch verb {
			case "install", "add", "i":
				return "this can run package lifecycle scripts"
			}
		case "pip", "pip3":
			if verb, ok := nextVerb(fields, i+1); ok && verb == "install" {
				return "this installs third-party code into the environment"
			}
		case "go":
			if verb, ok := nextVerb(fields, i+1); ok && verb == "install" {
				return "this builds and installs an upstream module"
			}
		case "cargo":
			if verb, ok := nextVerb(fields, i+1); ok && verb == "install" {
				return "this builds and installs upstream Rust code"
			}
		}
	}
	return ""
}

// nextVerb returns the first non-flag token after i.
func nextVerb(fields []string, i int) (string, bool) {
	for ; i < len(fields); i++ {
		if strings.HasPrefix(fields[i], "-") {
			continue
		}
		return strings.ToLower(fields[i]), true
	}
	return "", false
}
