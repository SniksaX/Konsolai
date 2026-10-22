package agent

import (
	"bytes"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/ui"
)

func TestUnverifiedClaimsKillWithoutShell(t *testing.T) {
	got := UnverifiedClaims("Done. Process killed.", nil)
	if len(got) != 1 || got[0] != "killed/stopped" {
		t.Fatalf("%v", got)
	}
	got = UnverifiedClaims("Process killed.", []VerifiedCall{{
		Tool: "run_shell", Args: `{"cmd":"kill 123"}`,
	}})
	if len(got) != 0 {
		t.Fatalf("verified %v", got)
	}
	got = UnverifiedClaims("Process killed.", []VerifiedCall{{
		Tool: "run_shell", Args: `{"cmd":"fuser -k 18765/tcp"}`,
	}})
	if len(got) != 0 {
		t.Fatalf("fuser verified %v", got)
	}
}

func TestUnverifiedClaimsCreateNeedsWrite(t *testing.T) {
	got := UnverifiedClaims("I created README.md", nil)
	if len(got) != 1 || got[0] != "created/wrote" {
		t.Fatalf("%v", got)
	}
	got = UnverifiedClaims("I created README.md", []VerifiedCall{{Tool: "write_file", Args: `{"path":"README.md"}`}})
	if len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestUnverifiedClaimsVenvAndRemovedImport(t *testing.T) {
	venv := "Created virtual environment at /work/.venv (Python 3.11.2, pip 23.0.1). Activate with: source .venv/bin/activate"
	got := UnverifiedClaims(venv, []VerifiedCall{{
		Tool: "run_shell", Args: `{"cmd":"python3 -m venv .venv"}`,
	}})
	if len(got) != 0 {
		t.Fatalf("venv %v", got)
	}
	got = UnverifiedClaims(venv, []VerifiedCall{{
		Tool: "run_shell", Args: `{"cmd":"python3 --version"}`,
	}})
	if len(got) != 1 || got[0] != "created/wrote" {
		t.Fatalf("readonly shell %v", got)
	}

	removed := `Fixed report.go: removed the unused "os" import.`
	got = UnverifiedClaims(removed, []VerifiedCall{{
		Tool: "edit_file", Args: `{"path":"report.go"}`, Out: "edited report.go (+0 -1)",
	}})
	if len(got) != 0 {
		t.Fatalf("removed import %v", got)
	}
	got = UnverifiedClaims(removed, []VerifiedCall{{
		Tool: "edit_file", Args: `{"path":"report.go"}`, Out: "edited report.go (+1 -0)",
	}})
	if len(got) != 1 || got[0] != "deleted" {
		t.Fatalf("edit without removal %v", got)
	}

	got = UnverifiedClaims("Process killed.", []VerifiedCall{{
		Tool: "run_shell", Args: `{"cmd":"python3 -m venv .venv"}`,
	}})
	if len(got) != 1 || got[0] != "killed/stopped" {
		t.Fatalf("kill without kill command %v", got)
	}
}

func TestFinishWarnsUnverifiedKill(t *testing.T) {
	var out bytes.Buffer
	_, _, err := setupCfg(t, []llm.Message{
		assistant(call("1", "finish", `{"summary":"Process killed."}`)),
	}, stubConfirm{d: ui.Approve}, Config{Out: &out, MaxSteps: 4, AutoApprove: true, Plain: true, HideStats: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "not verified: killed/stopped") {
		t.Fatalf("%q", out.String())
	}
}
