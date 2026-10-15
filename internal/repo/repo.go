package repo

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const maxFiles = 20000

func Root(cwd string) string {
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return cwd
		}
		dir = parent
	}
}

func Files(ctx context.Context, root string) ([]string, error) {
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		return gitFiles(ctx, root)
	}
	return walkFiles(root)
}

func gitFiles(ctx context.Context, root string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-co", "--exclude-standard", "-z")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("repo: git ls-files: %w", err)
	}
	var files []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		files = append(files, filepath.ToSlash(p))
	}
	sort.Strings(files)
	if len(files) > maxFiles {
		return nil, fmt.Errorf("repo: too many files (%d); cap is %d", len(files), maxFiles)
	}
	return files, nil
}

var skipDirs = map[string]struct{}{
	".git": {}, "node_modules": {}, "dist": {}, "build": {}, "target": {},
	"vendor": {}, ".venv": {}, "__pycache__": {}, ".next": {}, ".cache": {},
}

func walkFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if _, skip := skipDirs[name]; skip && path != root {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		if len(files) > maxFiles {
			return fmt.Errorf("repo: too many files (%d); cap is %d", len(files), maxFiles)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("repo: walk: %w", err)
	}
	sort.Strings(files)
	return files, nil
}

func Map(files []string, maxLines int) string {
	if maxLines <= 0 || len(files) == 0 {
		return ""
	}
	root := &node{kids: map[string]*node{}}
	for _, f := range files {
		cur := root
		parts := strings.Split(f, "/")
		for i, p := range parts {
			if cur.kids[p] == nil {
				cur.kids[p] = &node{kids: map[string]*node{}}
			}
			cur = cur.kids[p]
			cur.n++
			if i == len(parts)-1 {
				cur.file = true
			}
		}
	}
	names := sortedKeys(root.kids)
	var lines []string
	for _, name := range names {
		if len(lines) >= maxLines {
			break
		}
		ch := root.kids[name]
		if ch.file && len(ch.kids) == 0 {
			lines = append(lines, name)
			continue
		}
		lines = append(lines, fmt.Sprintf("%s/ (%d files)", name, ch.n))
		subs := sortedKeys(ch.kids)
		for _, sub := range subs {
			if len(lines) >= maxLines {
				break
			}
			g := ch.kids[sub]
			if g.file && len(g.kids) == 0 {
				lines = append(lines, "  "+sub)
			} else {
				lines = append(lines, fmt.Sprintf("  %s/ (%d files)", sub, g.n))
			}
		}
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return strings.Join(lines, "\n")
}

func sortedKeys(m map[string]*node) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type node struct {
	n    int
	kids map[string]*node
	file bool
}
