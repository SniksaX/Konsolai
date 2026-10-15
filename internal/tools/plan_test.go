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
