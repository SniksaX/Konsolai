package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/siraj/konsolai/internal/safety"
)

func TestProposePlanPreviewOrder(t *testing.T) {
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := NewRegistry(g, nil, nil, nil, nil).Get("propose_plan")
	if !ok {
		t.Fatal("missing propose_plan")
	}
	act, err := tool.Prepare(`{"summary":"init","steps":["npm init","install express"],"files":["package.json"],"commands":["npm init -y","npm install express"]}`)
	if err != nil {
		t.Fatal(err)
	}
	prev := act.Preview()
	if !strings.Contains(prev, "1. npm init") || !strings.Contains(prev, "files: package.json") || !strings.Contains(prev, "commands: npm init -y, npm install express") {
		t.Fatalf("%q", prev)
	}
	i1 := strings.Index(prev, "1.")
	i2 := strings.Index(prev, "files:")
	i3 := strings.Index(prev, "commands:")
	if !(i1 < i2 && i2 < i3) {
		t.Fatalf("order %q", prev)
	}
}

func TestProposePlanRequiresExportDocForDocsPDF(t *testing.T) {
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry(g, nil, nil, nil, nil, Options{DocsTask: true, Request: "create docs/OVERVIEW.PDF with diagrams"})
	tool, ok := reg.Get("propose_plan")
	if !ok {
		t.Fatal("missing propose_plan")
	}
	_, err = tool.Prepare(`{"summary":"write overview","steps":["write docs/OVERVIEW.md","render pdf"],"files":["docs/OVERVIEW.md"]}`)
	if err == nil || !strings.Contains(err.Error(), PlanExportReject) {
		t.Fatalf("want PlanExportReject, got %v", err)
	}
	act, err := tool.Prepare(`{"summary":"write md then export_doc","steps":["write docs/OVERVIEW.md","export_doc pdf"],"files":["docs/OVERVIEW.md","docs/OVERVIEW.pdf"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if act.Kind() != Plan {
		t.Fatal("want Plan")
	}
	plain, ok := NewRegistry(g, nil, nil, nil, nil).Get("propose_plan")
	if !ok {
		t.Fatal("missing plain propose_plan")
	}
	if _, err := plain.Prepare(`{"summary":"write overview","steps":["write docs/OVERVIEW.md"],"files":["docs/OVERVIEW.md"]}`); err != nil {
		t.Fatalf("non-docs should accept: %v", err)
	}
}

func TestProposePlanRejectsPlaceholderCommands(t *testing.T) {
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := NewRegistry(g, nil, nil, nil, nil).Get("propose_plan")
	if !ok {
		t.Fatal("missing")
	}
	bads := []string{
		`{"summary":"s","steps":["x"],"files":["a"],"commands":["mkdir (all dirs)"]}`,
		`{"summary":"s","steps":["x"],"files":["a"],"commands":["do ..."]}`,
		`{"summary":"s","steps":["x"],"files":["a"],"commands":["run listed commands"]}`,
		`{"summary":"s","steps":["x"],"files":["a"],"commands":["touch each file"]}`,
		`{"summary":"s","steps":["x"],"files":["a"],"commands":["npm i && etc"]}`,
	}
	for _, raw := range bads {
		_, err := tool.Prepare(raw)
		if err == nil || !strings.Contains(err.Error(), PlanCommandReject) {
			t.Fatalf("want reject for %s: %v", raw, err)
		}
	}
	if _, err := tool.Prepare(`{"summary":"s","steps":["x"],"files":["a"],"commands":["mkdir -p a/b"]}`); err != nil {
		t.Fatal(err)
	}
}

func TestProposePlanRejectsSudo(t *testing.T) {
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := NewRegistry(g, nil, nil, nil, nil).Get("propose_plan")
	if !ok {
		t.Fatal("missing")
	}
	_, err = tool.Prepare(`{"summary":"s","steps":["install"],"files":["a"],"commands":["sudo pacman -S foo"]}`)
	if err == nil || !strings.Contains(err.Error(), safety.PrivilegedPlanMsg) {
		t.Fatalf("sudo: %v", err)
	}
}

func TestProposePlanReadOnlyOmitsFiles(t *testing.T) {
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := NewRegistry(g, nil, nil, nil, nil).Get("propose_plan")
	if !ok {
		t.Fatal("missing")
	}
	raw := `{"summary":"health","steps":["go build ./...","go vet ./...","go test ./..."],"commands":["go build ./...","go vet ./...","go test ./..."]}`
	act, err := tool.Prepare(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := act.(interface {
		PlanFiles() []string
		PlanCommands() []string
	})
	if !ok {
		t.Fatal("plan accessors")
	}
	if len(p.PlanFiles()) != 0 {
		t.Fatalf("files %v", p.PlanFiles())
	}
	cmds := p.PlanCommands()
	if len(cmds) != 3 || cmds[0] != "go build ./..." || cmds[1] != "go vet ./..." || cmds[2] != "go test ./..." {
		t.Fatalf("commands %v", cmds)
	}
}

func TestProposePlanEditStillRequiresFiles(t *testing.T) {
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := NewRegistry(g, nil, nil, nil, nil).Get("propose_plan")
	if !ok {
		t.Fatal("missing")
	}
	_, err = tool.Prepare(`{"summary":"add a.txt","steps":["write a.txt"]}`)
	if err == nil || !strings.Contains(err.Error(), "files is required") {
		t.Fatalf("want files required, got %v", err)
	}
	if !strings.Contains(err.Error(), "files may be omitted: no") {
		t.Fatalf("shape %v", err)
	}
}

func TestProposePlanProseShowsShape(t *testing.T) {
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := NewRegistry(g, nil, nil, nil, nil).Get("propose_plan")
	if !ok {
		t.Fatal("missing")
	}
	_, err = tool.Prepare("Read-only health check of the repo: build, vet, and test status.")
	if err == nil {
		t.Fatal("want reject")
	}
	e := err.Error()
	for _, s := range []string{
		"summary", "steps", "commands", "files",
		"go build ./...", "go vet ./...", "go test ./...",
		"files may be omitted: yes",
		"example:",
	} {
		if !strings.Contains(e, s) {
			t.Fatalf("missing %q in %s", s, e)
		}
	}
}

func TestProposePlanSchemaFilesOptional(t *testing.T) {
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := NewRegistry(g, nil, nil, nil, nil).Get("propose_plan")
	if !ok {
		t.Fatal("missing")
	}
	var schema struct {
		Required   []string `json:"required"`
		Properties struct {
			Files struct {
				Description string `json:"description"`
			} `json:"files"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Spec().Function.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	for _, r := range schema.Required {
		if r == "files" {
			t.Fatalf("files still required: %v", schema.Required)
		}
	}
	if !strings.Contains(schema.Properties.Files.Description, "Omit") {
		t.Fatalf("desc %q", schema.Properties.Files.Description)
	}
}
