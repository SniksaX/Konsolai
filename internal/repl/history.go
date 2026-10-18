package repl

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/SniksaX/Konsolai/internal/memory"
	"github.com/SniksaX/Konsolai/internal/redact"
)

const histCap = 1000

func HistoryPath() string {
	return filepath.Join(memory.StateDir(), "repl_history")
}

func ShellHistoryPath() string {
	return filepath.Join(memory.StateDir(), "shell_history")
}

type fileHistory struct {
	path  string
	lines []string
}

func loadFileHistory(path string) *fileHistory {
	h := &fileHistory{path: path}
	f, err := os.Open(path)
	if err != nil {
		return h
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line != "" {
			h.lines = append(h.lines, line)
		}
	}
	if len(h.lines) > histCap {
		h.lines = h.lines[len(h.lines)-histCap:]
	}
	return h
}

func (h *fileHistory) Write(line string) (int, error) {
	line = redact.String(strings.TrimRight(line, "\n"))
	if strings.TrimSpace(line) == "" {
		return len(h.lines), nil
	}
	h.lines = append(h.lines, line)
	if len(h.lines) > histCap {
		h.lines = h.lines[len(h.lines)-histCap:]
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0700); err != nil {
		return len(h.lines), err
	}
	f, err := os.OpenFile(h.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return len(h.lines), err
	}
	defer f.Close()
	for _, l := range h.lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			return len(h.lines), err
		}
	}
	_ = f.Chmod(0600)
	return len(h.lines), nil
}

func (h *fileHistory) GetLine(pos int) (string, error) {
	if pos < 0 || pos >= len(h.lines) {
		return "", nil
	}
	return h.lines[pos], nil
}

func (h *fileHistory) Len() int { return len(h.lines) }

func (h *fileHistory) Dump() interface{} { return h.lines }
