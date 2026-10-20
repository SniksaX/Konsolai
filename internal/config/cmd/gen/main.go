package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/SniksaX/Konsolai/internal/config"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	if err := write(filepath.Join(root, "config.example.yaml"), config.InitYAML()); err != nil {
		fail(err)
	}
	if err := write(filepath.Join(root, "config.default.yaml"), config.InitYAML()); err != nil {
		fail(err)
	}
	// Config keys come from EnvExample. Compose-only variables are a separate block.
	if err := write(filepath.Join(root, ".env.example"), config.EnvExample()+config.ComposeEnvExample()); err != nil {
		fail(err)
	}
}

func write(path, body string) error {
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		return fmt.Errorf("gen: write %s: %w", path, err)
	}
	return nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
