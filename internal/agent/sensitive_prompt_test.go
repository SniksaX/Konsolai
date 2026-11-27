package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/ui"
)

type sensitiveConfirmSeq struct {
	decisions []ui.Decision
	calls     []string
	previews  []string
}

func (s *sensitiveConfirmSeq) next(summary string) (ui.Decision, string, error) {
	s.calls = append(s.calls, summary)
	if len(s.decisions) == 0 {
		return ui.Reject, "", nil
	}
	d := s.decisions[0]
	s.decisions = s.decisions[1:]
	return d, "", nil
}

func (s *sensitiveConfirmSeq) Confirm(summary, preview string) (ui.Decision, string, error) {
	s.previews = append(s.previews, preview)
	return s.next(summary)
}
func (s *sensitiveConfirmSeq) ConfirmForced(summary, preview string) (ui.Decision, string, error) {
	s.previews = append(s.previews, preview)
	return s.next(summary)
}

func TestAutoApproveRefusesCodeRunningWrite(t *testing.T) {
	root, rep, err := setupCfg(t, []llm.Message{
		assistant(call("1", "write_file", `{"path":"scripts/x.sh","content":"echo hi"}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	}, &sensitiveConfirmSeq{decisions: []ui.Decision{ui.Approve}}, Config{AutoApprove: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "scripts", "x.sh")); !os.IsNotExist(err) {
		t.Fatal("--yes must refuse a code-running write")
	}
	if got := rep.lastTool().Content; !strings.Contains(got, "refused:") || !strings.Contains(got, "runs code later") {
		t.Fatalf("tool result %q", got)
	}
}

func TestCodeRunningPromptIsSeparateFromApproveAll(t *testing.T) {
	confirm := &sensitiveConfirmSeq{decisions: []ui.Decision{ui.ApproveAll, ui.Approve}}
	root, _, err := setupCfg(t, []llm.Message{
		assistant(call("p", "propose_plan", `{"summary":"write a script","steps":["write it"],"files":["scripts/x.sh"],"commands":["ls scripts"]}`)),
		assistant(call("w", "write_file", `{"path":"scripts/x.sh","content":"echo hi"}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	}, confirm, Config{RequirePlan: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(confirm.calls) < 2 {
		t.Fatalf("approve-all skipped the separate prompt: %v", confirm.calls)
	}
	if !strings.Contains(confirm.calls[1], "runs code later") {
		t.Fatalf("second prompt %q", confirm.calls[1])
	}
	if _, err := os.Stat(filepath.Join(root, "scripts", "x.sh")); err != nil {
		t.Fatalf("approved write missing: %v", err)
	}
}

func TestCodeRunningPromptRejectSkipsWrite(t *testing.T) {
	confirm := &sensitiveConfirmSeq{decisions: []ui.Decision{ui.Approve, ui.Reject}}
	root, rep, err := setupCfg(t, []llm.Message{
		assistant(call("p", "propose_plan", `{"summary":"write a script","steps":["write it"],"files":["scripts/x.sh"],"commands":["ls scripts"]}`)),
		assistant(call("w", "write_file", `{"path":"scripts/x.sh","content":"echo hi"}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	}, confirm, Config{RequirePlan: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "scripts", "x.sh")); !os.IsNotExist(err) {
		t.Fatal("rejected code-running write must not run")
	}
	if got := rep.lastTool().Content; !strings.Contains(got, "rejected") {
		t.Fatalf("tool result %q", got)
	}
}

func TestSensitivePromptRedactsSecrets(t *testing.T) {
	secret := "sk-" + "abcdefghijklmnopqrstuvwx"
	confirm := &sensitiveConfirmSeq{decisions: []ui.Decision{ui.Approve, ui.Approve}}
	root, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "write_file", `{"path":"scripts/x.sh","content":"echo `+secret+`"}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	}, confirm, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(confirm.previews) == 0 {
		t.Fatal("no sensitive prompt")
	}
	// The code-running prompt is the separate one; its reason and diff must
	// not echo the secret value.
	last := confirm.previews[len(confirm.previews)-1]
	if strings.Contains(last, secret) {
		t.Fatalf("code-running prompt leaks the secret: %q", last)
	}
	if _, err := os.Stat(filepath.Join(root, "scripts", "x.sh")); err != nil {
		t.Fatalf("approved sensitive write missing: %v", err)
	}
}

func TestInstallNoteAppearsInThePrompt(t *testing.T) {
	confirm := &sensitiveConfirmSeq{decisions: []ui.Decision{ui.Approve}}
	_, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "run_shell", `{"cmd":"npm install left-pad"}`)),
		{Role: llm.RoleAssistant, Content: "done"},
	}, confirm, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(confirm.previews) == 0 || !strings.Contains(confirm.previews[0], "lifecycle scripts") {
		t.Fatalf("install note missing from prompt: %v", confirm.previews)
	}
}
