package agent

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/safety"
	"github.com/SniksaX/Konsolai/internal/tools"
	"github.com/SniksaX/Konsolai/internal/ui"
)

func TestThinkingCapKeepsPartialPlan(t *testing.T) {
	root := t.TempDir()
	g, err := safety.New(root)
	if err != nil {
		t.Fatal(err)
	}
	client, rep := newReplay(t, []llm.Message{
		assistant(call("1", "propose_plan", `{"summary":"partial","steps":["a"],"commands":["true"]}`)),
		assistant(call("2", "finish", `{"summary":"kept the partial plan"}`)),
	})
	rep.truncates = []bool{true}
	err = Run(context.Background(), Config{
		Client: client, Tools: tools.NewRegistry(g, io.Discard, nil, nil, nil),
		Confirmer: stubConfirm{d: ui.Approve}, Out: io.Discard, MaxSteps: 6,
		Intent: "act", RequirePlan: true, ThinkingPolicy: ThinkOn,
	}, "sys", "task")
	if err != nil {
		t.Fatal(err)
	}
	got := rep.lastTool()
	if !strings.Contains(got.Content, "plan approved") {
		t.Fatalf("partial plan was not kept: %+v", got)
	}
}
