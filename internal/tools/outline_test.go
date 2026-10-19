package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/safety"
)

func TestOutlineGoFile(t *testing.T) {
	root, reg := codeReg(t)
	src := `package demo

import "fmt"

const Max = 3

var n int

type Box struct {
	W int
}

func (b Box) Area() int { return b.W }

func Add(a, b int) int { return a + b }
`
	if err := os.WriteFile(filepath.Join(root, "calc.go"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	tool, _ := reg.Get("outline")
	act, err := tool.Prepare(`{"path":"calc.go"}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Output)
	}
	for _, want := range []string{"package demo", "func Add(a, b int) int", "type Box", "const Max", "var n", "func (Box) Area() int"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("missing %q in %q", want, res.Output)
		}
	}
	if !strings.Contains(res.Meta, "decls") {
		t.Fatalf("meta %q", res.Meta)
	}
}

func TestOutlineDirUsesLister(t *testing.T) {
	root, reg := codeReg(t)
	if err := os.WriteFile(filepath.Join(root, "a.py"), []byte("def foo():\n    pass\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tool, _ := reg.Get("outline")
	act, err := tool.Prepare(`{"path":"."}`)
	if err != nil {
		t.Fatal(err)
	}
	res, err := act.Run(context.Background())
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Output)
	}
	if !strings.Contains(res.Output, "def foo") {
		t.Fatalf("%q", res.Output)
	}
}

func TestOutlineMissingPath(t *testing.T) {
	g, err := safety.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool := newOutline(g, nil)
	if _, err := tool.Prepare(`{}`); err == nil {
		t.Fatal("want error")
	}
}
