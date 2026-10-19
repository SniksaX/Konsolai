package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// ResolveConfigPath is --config, then KONSOLAI_CONFIG, then the XDG default.
func ResolveConfigPath(flag string) string {
	if s := stringsTrim(flag); s != "" {
		return s
	}
	if s := stringsTrim(os.Getenv("KONSOLAI_CONFIG")); s != "" {
		return s
	}
	return DefaultPath()
}

func stringsTrim(s string) string {
	i := 0
	j := len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\n') {
		j--
	}
	return s[i:j]
}

// RealPath is path after symlink resolution. A missing path is returned as given.
func RealPath(path string) (string, error) {
	if path == "" {
		path = DefaultPath()
	}
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return path, nil
		}
		return "", fmt.Errorf("config: stat %s: %w", path, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("config: resolve %s: %w", path, err)
	}
	return real, nil
}

// WriteAtomic writes data to the real file behind path.
// A symlink stays a symlink; the bytes land in the target's directory.
// The file mode is 0600.
func WriteAtomic(path string, data []byte) error {
	if path == "" {
		path = DefaultPath()
	}
	dest, err := RealPath(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return fmt.Errorf("config: mkdir: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(dest), ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("config: temp %s: %w", dest, err)
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("config: write %s: %w", dest, err)
	}
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return fmt.Errorf("config: chmod %s: %w", dest, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("config: close %s: %w", dest, err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("config: rename %s: %w", dest, err)
	}
	ok = true
	return nil
}
