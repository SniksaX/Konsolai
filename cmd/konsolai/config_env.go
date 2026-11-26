package main

import (
	"os"
	"path/filepath"
	"strings"
)

// setActiveConfigFromFlag makes a --config path the active configuration for
// the whole process, so the tool and agent protection resolves the same file
// the loader opened. The returned value is the resolved absolute path.
func setActiveConfigFromFlag(flag string) string {
	path := strings.TrimSpace(flag)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	_ = os.Setenv("KONSOLAI_CONFIG", path)
	return path
}
