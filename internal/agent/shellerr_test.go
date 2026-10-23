package agent

import (
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
)

func TestMissingCwdFinishesTurn(t *testing.T) {
	_, rep, err := setup(t, []llm.Message{
		assistant(call("1", "run_shell", `{"cmd":"pwd","cwd":"missing-dir"}`)),
		assistant(call("2", "finish", `{"summary":"could not enter the directory"}`)),
	}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := rep.lastTool()
	if got.Role != llm.RoleTool || !strings.Contains(got.Content, "cwd does not exist:") {
		t.Fatalf("last tool %+v", got)
	}
	if strings.Contains(got.Content, "fork/exec") {
		t.Fatalf("misleading exec error: %s", got.Content)
	}
}
