package repo

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const instructionsCap = 16 * 1024

type Project struct {
	Kinds    []string
	Commands []string
}

type pkgJSON struct {
	Scripts map[string]string `json:"scripts"`
}

func Detect(root string) Project {
	var p Project
	if fileExists(filepath.Join(root, "go.mod")) {
		p.Kinds = append(p.Kinds, "go")
		p.Commands = append(p.Commands, "go build ./...", "go vet ./...", "go test ./...")
	}
	if fileExists(filepath.Join(root, "package.json")) {
		p.Kinds = append(p.Kinds, "node")
		p.Commands = append(p.Commands, nodeCommands(root)...)
	}
	if fileExists(filepath.Join(root, "Cargo.toml")) {
		p.Kinds = append(p.Kinds, "rust")
		p.Commands = append(p.Commands, "cargo build", "cargo test")
	}
	if fileExists(filepath.Join(root, "pyproject.toml")) {
		p.Kinds = append(p.Kinds, "python")
	}
	if fileExists(filepath.Join(root, "Makefile")) {
		p.Kinds = append(p.Kinds, "make")
	}
	return p
}

func nodeCommands(root string) []string {
	b, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var pkg pkgJSON
	if err := json.Unmarshal(b, &pkg); err != nil {
		return nil
	}
	var cmds []string
	hasTypecheck := false
	for _, name := range []string{"build", "test", "lint", "typecheck"} {
		if _, ok := pkg.Scripts[name]; ok {
			cmds = append(cmds, "npm run "+name)
			if name == "typecheck" {
				hasTypecheck = true
			}
		}
	}
	if !hasTypecheck && fileExists(filepath.Join(root, "tsconfig.json")) {
		cmds = append(cmds, "npx tsc --noEmit")
	}
	return cmds
}

func Instructions(root string) (string, error) {
	for _, name := range []string{"KONSOLAI.md", "AGENTS.md"} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		if len(b) > instructionsCap {
			b = b[:instructionsCap]
		}
		return string(b), nil
	}
	return "", nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
