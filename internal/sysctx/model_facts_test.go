package sysctx

import (
	"strings"
	"testing"
)

func TestModelFactsHoldTheRealModels(t *testing.T) {
	s := Snapshot{
		OS: "Linux", CWD: "/tmp/proj",
		Model: ModelFacts{
			MainName: "deepseek-flash", MainID: "deepseek-chat",
			MainHost: "api.deepseek.com", MainRemote: true,
			FastName: "fast", FastID: "llama3.2:3b",
			FastHost: "fastbox:8080", FastRemote: false, HasFast: true,
			Role: "fast",
		},
	}
	full := s.String()
	for _, want := range []string{
		"deepseek-flash", "deepseek-chat", "api.deepseek.com", "remote",
		"fast", "llama3.2:3b", "fastbox:8080", "local",
		"answered by the fast role",
		`say "I don't know"`,
	} {
		if !strings.Contains(full, want) {
			t.Fatalf("the model facts are missing %q:\n%s", want, full)
		}
	}
}

func TestModelFactsNameTheServerDefaultID(t *testing.T) {
	facts := ModelFacts{MainName: "home", MainHost: "127.0.0.1:8000", Role: "main"}
	got := facts.String()
	if !strings.Contains(got, "id server default") {
		t.Fatalf("empty id is not named: %q", got)
	}
	if !strings.Contains(got, "fast role: not configured") {
		t.Fatalf("absent fast role is not named: %q", got)
	}
	if strings.Contains(got, "remote") {
		t.Fatalf("a local model is marked remote: %q", got)
	}
}

func TestEmptyModelFactsStayOutOfTheContext(t *testing.T) {
	s := Snapshot{OS: "Linux", Shell: "bash", User: "dev", CWD: "/tmp/proj"}
	if got := s.Model.String(); got != "" {
		t.Fatalf("empty facts rendered: %q", got)
	}
	if strings.Contains(s.String(), "Model facts") {
		t.Fatalf("empty facts leaked into the context: %q", s.String())
	}
}
