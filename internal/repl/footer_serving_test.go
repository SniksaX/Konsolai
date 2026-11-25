package repl

import (
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/agent"
	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/turn"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
)

func servingFooter(st *session, r *turn.Runner, prof config.Profile) string {
	return theme.StripANSI(sessionFooter(st, Config{Runner: r, Env: &commands.Env{Profile: prof}}))
}

// TestFooterShowsTheServingModel proves the footer names the model that served
// the last call: a fast answer shows the fast model and role, never the main one.
func TestFooterShowsTheServingModel(t *testing.T) {
	t.Setenv("COLUMNS", "120")
	prof := config.Builtin()
	prof.Models["cloud"] = config.Model{Endpoint: "http://203.0.113.9/v1", Model: "remote-model"}
	prof.Roles.Main = "cloud"
	st := &session{cwd: "/tmp/x"}

	fast := servingFooter(st, &turn.Runner{Serving: agent.Model{
		Name: "fast", ID: "llama3.2:3b", Role: "fast",
	}}, prof)
	if !strings.Contains(fast, "llama3.2:3b") || !strings.Contains(fast, "fast") {
		t.Fatalf("a fast answer is not named in the footer: %q", fast)
	}
	if strings.Contains(fast, "remote-model") || strings.Contains(fast, "remote") {
		t.Fatalf("a fast answer looks like the remote main model: %q", fast)
	}

	main := servingFooter(st, &turn.Runner{Serving: agent.Model{
		Name: "cloud", ID: "remote-model", Role: "main", Remote: true,
	}}, prof)
	for _, want := range []string{"remote-model", "main", "remote"} {
		if !strings.Contains(main, want) {
			t.Fatalf("the main answer misses %q in the footer: %q", want, main)
		}
	}

	facts := servingFooter(st, &turn.Runner{Serving: agent.Model{Name: "facts", Role: "local"}}, prof)
	if !strings.Contains(facts, "facts") || !strings.Contains(facts, "local") {
		t.Fatalf("the local facts answer is not named in the footer: %q", facts)
	}

	// Before the first turn the footer falls back to the configured main model.
	fallback := servingFooter(st, &turn.Runner{}, prof)
	for _, want := range []string{"remote-model", "remote"} {
		if !strings.Contains(fallback, want) {
			t.Fatalf("the fallback footer misses %q: %q", want, fallback)
		}
	}
}
