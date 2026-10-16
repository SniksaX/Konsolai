package tools

import (
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
	if err == nil || err.Error() != PlanExportReject {
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
