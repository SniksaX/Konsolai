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
	RepoRoot  string
	OS        string
	Shell     string
	User      string
	Entries   []string
	Truncated bool
	GitRepo   bool
	Pkg       PkgInfo
	Model     ModelFacts
}

// ModelFacts is the chat model setup of the running session. It is the only
// source the model may use for identity and setup questions.
type ModelFacts struct {
	MainName   string // configured main model name
	MainID     string // configured main model id; empty means the server default is not chosen
	MainHost   string // main endpoint host
	MainRemote bool
	FastName   string
	FastID     string
	FastHost   string
	FastRemote bool
	HasFast    bool
	Role       string // role answering the current call: main or fast
}

func (m ModelFacts) empty() bool {
	return m.MainName == "" && m.MainID == "" && m.MainHost == "" &&
		!m.HasFast && m.Role == ""
}

// String renders the model facts block. Empty facts render nothing.
func (m ModelFacts) String() string {
	if m.empty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("Model facts (the only source for identity and setup answers):\n")
	b.WriteString("- Konsolai is a local coding agent. The chat model is served by the endpoint listed here.\n")
	fmt.Fprintf(&b, "- main role: model %s, id %s, endpoint host %s, %s.\n",
		factText(m.MainName), factID(m.MainID), factText(m.MainHost), factLocality(m.MainRemote))
	if m.HasFast {
		fmt.Fprintf(&b, "- fast role: model %s, id %s, endpoint host %s, %s.\n",
			factText(m.FastName), factID(m.FastID), factText(m.FastHost), factLocality(m.FastRemote))
	} else {
		b.WriteString("- fast role: not configured; answers use main.\n")
	}
	if m.Role != "" {
		fmt.Fprintf(&b, "- This call is answered by the %s role.\n", m.Role)
	}
	b.WriteString("Answer identity or setup questions only from these facts. If a fact is not listed, say \"I don't know\".")
	return b.String()
}

func factText(s string) string {
	if strings.TrimSpace(s) == "" {
		return "not configured"
	}
	return s
}

func factID(s string) string {
	if strings.TrimSpace(s) == "" {
		return "id not set"
	}
	return s
}

func factLocality(remote bool) string {
	if remote {
		return "remote"
	}
	return "local"
}

func LocationLine(cwd, root string) string {
	rel, err := filepath.Rel(root, cwd)
	if err != nil {
		rel = "."
	}
	rel = filepath.ToSlash(rel)
	if rel == "" {
		rel = "."
	}
	return fmt.Sprintf("Current directory: %s (repo root: %s, you are in %s/)", cwd, root, rel)
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
		Pkg:     detectPkg(),
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
	if line := s.Pkg.Line(); line != "" {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if s.RepoRoot != "" {
		b.WriteString(LocationLine(s.CWD, s.RepoRoot))
		b.WriteByte('\n')
	}
	b.WriteString("Directory entries: ")
	if len(s.Entries) == 0 {
		b.WriteString("(empty)")
	} else {
		b.WriteString(strings.Join(s.Entries, " "))
		if s.Truncated {
			b.WriteString(" ...")
		}
	}
	if facts := s.Model.String(); facts != "" {
		b.WriteString("\n\n")
		b.WriteString(facts)
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
