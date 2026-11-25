package agent

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/tools"
	"github.com/SniksaX/Konsolai/internal/ui"
)

func TestModelLabelNamesTheNameRoleAndRemote(t *testing.T) {
	cases := []struct {
		model Model
		want  string
	}{
		{Model{Name: "deepseek-flash", ID: "deepseek-chat", Role: "main", Remote: true},
			"deepseek-chat (main) remote"},
		{Model{Name: "fast", ID: "llama3.2:3b", Role: "fast"},
			"llama3.2:3b (fast)"},
		{Model{Name: "fallback-name", Role: "main"}, "fallback-name (main)"},
		{Model{Name: "facts", Role: "local"}, "facts (local)"},
	}
	for _, tc := range cases {
		if got := tc.model.Label(); got != tc.want {
			t.Errorf("Label(%+v) = %q, want %q", tc.model, got, tc.want)
		}
	}
	if (Model{}).Valid() {
		t.Fatal("an empty model must not be valid")
	}
}

func TestFormatTraceServingPrefixesTheModel(t *testing.T) {
	serving := Model{Name: "deepseek-flash", ID: "deepseek-chat", Role: "main", Remote: true}
	got := FormatTraceServing(serving, 1, false, time.Second, 100, 20, 0, 8192, nil)
	if !strings.Contains(got, "model deepseek-chat (main) remote") || !strings.Contains(got, "[step 1]") {
		t.Fatalf("trace %q", got)
	}
	plain := FormatTraceServing(Model{}, 1, false, time.Second, 100, 20, 0, 8192, nil)
	if strings.Contains(plain, "model ") || !strings.HasPrefix(plain, "  [step 1]") {
		t.Fatalf("empty attribution changed the trace: %q", plain)
	}
}

func TestFormatStatsAndSummaryCarryTheServingModel(t *testing.T) {
	serving := Model{Name: "fast", ID: "llama3.2:3b", Role: "fast"}
	st := Stats{Steps: 1, Serving: serving, MaxStep: time.Second, PromptTokens: 10, CompletionTokens: 2}
	line := FormatStatsLine(st)
	if !strings.Contains(line, "model=llama3.2:3b (fast)") {
		t.Fatalf("stats line %q", line)
	}
	summary := FormatSummary("answer", st)
	if !strings.HasPrefix(summary, "model llama3.2:3b (fast) ·") {
		t.Fatalf("summary %q", summary)
	}
	remote := FormatSummary("answer", Stats{Serving: Model{Name: "cloud", ID: "remote-model", Role: "main", Remote: true}})
	if !strings.Contains(remote, "model remote-model (main) remote") {
		t.Fatalf("remote summary %q", remote)
	}
	if empty := FormatSummary("answer", Stats{PromptTokens: 10, CompletionTokens: 2}); strings.Contains(empty, "model ") {
		t.Fatalf("empty attribution changed the summary: %q", empty)
	}
}

// TestAgentRunNamesTheServingModel proves the agent trace and stats lines name
// the model the caller says served the call.
func TestAgentRunNamesTheServingModel(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	client, _ := newReplay(t, []llm.Message{{Role: llm.RoleAssistant, Content: "done"}})
	serving := Model{Name: "cloud", ID: "remote-model", Role: "main", Remote: true}
	var errb bytes.Buffer
	st := &Stats{}
	err = Run(context.Background(), Config{
		Client:    client,
		Tools:     tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve},
		Out:       io.Discard,
		Err:       &errb,
		MaxSteps:  5,
		Trace:     true,
		Serving:   serving,
		Stats:     st,
		Intent:    "act",
	}, "sys", "do the task")
	if err != nil {
		t.Fatal(err)
	}
	if st.Serving != serving {
		t.Fatalf("stats serving %+v", st.Serving)
	}
	text := errb.String()
	if !strings.Contains(text, "model remote-model (main) remote") {
		t.Fatalf("trace/stats miss the serving model: %q", text)
	}
	if !strings.Contains(text, "model=remote-model (main) remote") {
		t.Fatalf("stats line misses model=: %q", text)
	}
}
