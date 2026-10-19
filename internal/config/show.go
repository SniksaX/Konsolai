package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type showView struct {
	LLM      LLM      `yaml:"llm"`
	Fast     Fast     `yaml:"fast"`
	Database Database `yaml:"database"`
	Memory   Memory   `yaml:"memory"`
	Context  Context  `yaml:"context"`
	Agent    Agent    `yaml:"agent"`
	RAG      RAG      `yaml:"rag"`
	UI       UI       `yaml:"ui"`
	Language Language `yaml:"language"`
	Safety   Safety   `yaml:"safety"`
	Router   Router   `yaml:"router"`
	Tools    Tools    `yaml:"tools"`
	Store    Store    `yaml:"store"`
	Stats    Stats    `yaml:"stats"`
}

func (c Config) ShowYAML() (string, error) {
	return renderShow(c), nil
}

// RenderResolved is the commented config document for c.
func RenderResolved(c Config) string {
	return renderCurrentYAML(c)
}

func WriteInit(path string, force bool) error {
	return writeConfigFile(path, InitYAML(), force)
}

// WriteCurrent writes the effective configuration, including values that
// came from the environment. It refuses to overwrite an existing file
// unless force is set.
func WriteCurrent(path string, force bool) error {
	if path == "" {
		path = DefaultPath()
	}
	f, err := Load(path)
	if err != nil {
		return err
	}
	c, err := f.Resolve("")
	if err != nil {
		return err
	}
	return writeConfigFile(path, renderCurrentYAML(c), force)
}

func writeConfigFile(path, body string, force bool) error {
	if path == "" {
		path = DefaultPath()
	}
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("config: %s already exists (use --force to overwrite)", path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("config: mkdir: %w", err)
	}
	return WriteAtomic(path, []byte(body))
}

func InitYAML() string {
	return renderInitYAML()
}
