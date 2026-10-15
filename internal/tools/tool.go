package tools

import (
	"context"
	"io"

	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/safety"
)

type ActionKind int

const (
	ReadOnly ActionKind = iota
	Mutating
	Plan
	Final
)

type FileLister func(ctx context.Context) ([]string, error)

type Tool interface {
	Spec() llm.ToolSpec
	Prepare(rawArgs string) (Action, error)
}

type Action interface {
	Summary() string
	Preview() string
	Kind() ActionKind
	Run(ctx context.Context) (Result, error)
}

type Result struct {
	Output  string
	IsError bool
	Final   bool
	Meta    string
}

type Registry struct {
	tools  []Tool
	byName map[string]Tool
}

func NewRegistry(g *safety.Guard, out io.Writer, files FileLister) *Registry {
	if files == nil {
		files = func(context.Context) ([]string, error) { return nil, nil }
	}
	list := []Tool{
		newShell(g, out),
		newWriteFile(g),
		newReadFile(g),
		newListDir(g),
		newSearch(g, files),
		newFindFiles(files),
		newEditFile(g),
		newProposePlan(),
		newFinish(),
	}
	byName := make(map[string]Tool, len(list))
	for _, t := range list {
		byName[t.Spec().Function.Name] = t
	}
	return &Registry{tools: list, byName: byName}
}

func (r *Registry) Specs() []llm.ToolSpec {
	specs := make([]llm.ToolSpec, len(r.tools))
	for i, t := range r.tools {
		specs[i] = t.Spec()
	}
	return specs
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}
