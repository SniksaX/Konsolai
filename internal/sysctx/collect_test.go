package sysctx

import (
	"strings"
	"testing"
)

func TestBriefOmitsRepoAndFacts(t *testing.T) {
	s := Snapshot{
		OS:      "Arch Linux",
		Shell:   "bash",
		User:    "dev",
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

func TestLocationLine(t *testing.T) {
	got := LocationLine("/tmp/proj/svc", "/tmp/proj")
	want := "Current directory: /tmp/proj/svc (repo root: /tmp/proj, you are in svc/)"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	got = LocationLine("/tmp/proj", "/tmp/proj")
	want = "Current directory: /tmp/proj (repo root: /tmp/proj, you are in ./)"
	if got != want {
		t.Fatalf("root: got %q want %q", got, want)
	}
	s := Snapshot{OS: "Arch Linux", Shell: "bash", User: "dev", CWD: "/tmp/proj/svc", RepoRoot: "/tmp/proj"}
	full := s.String()
	if !strings.Contains(full, "you are in svc/") {
		t.Fatalf("string missing location: %q", full)
	}
	s.Pkg = PkgInfo{Family: "Arch", System: "pacman", AUR: "yay"}
	full = s.String()
	if !strings.Contains(full, "Package manager: pacman (Arch family); AUR helper: yay.") {
		t.Fatalf("pkg line: %q", full)
	}
}
