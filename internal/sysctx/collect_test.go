package sysctx

import (
	"strings"
	"testing"
)

func TestBriefOmitsRepoAndFacts(t *testing.T) {
	s := Snapshot{
		OS:      "Arch Linux",
		Shell:   "bash",
		User:    "siraj",
		CWD:     "/tmp/proj",
		GitRepo: true,
		Entries: []string{"go.mod", "README.md"},
	}
	got := s.Brief()
	if got != "OS: Arch Linux\nShell: bash\nCWD: /tmp/proj" {
		t.Fatalf("%q", got)
	}
	full := s.String()
	for _, part := range []string{"User:", "Git repo:", "Directory entries:"} {
		if !strings.Contains(full, part) {
			t.Fatalf("full missing %q: %q", part, full)
		}
	}
}
