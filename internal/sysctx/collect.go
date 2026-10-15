package sysctx

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const maxEntries = 40

type Snapshot struct {
	CWD       string
	OS        string
	Shell     string
	User      string
	Entries   []string
	Truncated bool
	GitRepo   bool
}

func Collect() (Snapshot, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return Snapshot{}, fmt.Errorf("sysctx: getwd: %w", err)
	}
	s := Snapshot{
		CWD:     cwd,
		OS:      osName(),
		Shell:   filepath.Base(os.Getenv("SHELL")),
		User:    os.Getenv("USER"),
		GitRepo: inGitRepo(cwd),
	}
	// An unreadable directory is not fatal; the listing is best-effort context.
	if entries, err := os.ReadDir(cwd); err == nil {
		for _, e := range entries {
			if len(s.Entries) == maxEntries {
				s.Truncated = true
				break
			}
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			s.Entries = append(s.Entries, name)
		}
	}
	return s, nil
}

func (s Snapshot) Brief() string {
	return fmt.Sprintf("OS: %s\nShell: %s\nCWD: %s", s.OS, s.Shell, s.CWD)
}

func (s Snapshot) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "OS: %s\nShell: %s\nUser: %s\nCWD: %s\nGit repo: %t\n", s.OS, s.Shell, s.User, s.CWD, s.GitRepo)
	b.WriteString("Directory entries: ")
	if len(s.Entries) == 0 {
		b.WriteString("(empty)")
	} else {
		b.WriteString(strings.Join(s.Entries, " "))
		if s.Truncated {
			b.WriteString(" ...")
		}
	}
	return b.String()
}

func osName() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return runtime.GOOS
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return runtime.GOOS
}

func inGitRepo(dir string) bool {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}
