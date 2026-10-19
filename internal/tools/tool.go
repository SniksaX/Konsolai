package tools

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/safety"
)

type ActionKind int

const (
	ReadOnly ActionKind = iota
	Mutating
	Plan
	Final
)

type FileLister func(ctx context.Context) ([]string, error)

type FileChange struct {
	Path          string
	ExistedBefore bool
	Before        []byte
	AfterHash     string
	Diff          string
}

type WebCache interface {
	Get(ctx context.Context, url string) (title, text string, ok bool)
	Put(ctx context.Context, url, title, text string, ttl time.Duration) error
}

type NopWebCache struct{}

func (NopWebCache) Get(context.Context, string) (string, string, bool) { return "", "", false }
func (NopWebCache) Put(context.Context, string, string, string, time.Duration) error {
	return nil
}

type FactStore interface {
	Add(ctx context.Context, fact, scope string) error
}

type NopFactStore struct{}

func (NopFactStore) Add(context.Context, string, string) error { return nil }

func (k ActionKind) String() string {
	switch k {
	case ReadOnly:
		return "readonly"
	case Mutating:
		return "mutating"
	case Plan:
		return "plan"
	case Final:
		return "final"
	default:
		return "unknown"
	}
}

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
	Change  *FileChange
}

type Options struct {
	ReadOnlyShell       bool
	DocsTask            bool
	Request             string
	OnWebFetch          func(url, title, text string)
	Recall              RecallFunc
	DisplayLines        int
	ShellDefaultTimeout time.Duration
	ShellMaxTimeout     time.Duration
	ShellWaitDelay      time.Duration
	DisableWeb          bool
}

const DocsSourceReject = "docs tasks may not create or modify source files; write Markdown and use export_doc for PDF/HTML"

func DocsPathAllowed(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".txt", ".mmd", ".svg", ".png", ".csv", ".json", ".yaml", ".yml":
		return true
	}
	return false
}

type Registry struct {
	tools  []Tool
	byName map[string]Tool
}

func NewRegistry(g *safety.Guard, out io.Writer, files FileLister, cache WebCache, facts FactStore, opts ...Options) *Registry {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	if files == nil {
		files = func(context.Context) ([]string, error) { return nil, nil }
	}
	if cache == nil {
		cache = NopWebCache{}
	}
	if facts == nil {
		facts = NopFactStore{}
	}
	list := []Tool{
		newShellLimit(g, out, o.ReadOnlyShell, o.DocsTask, o.DisplayLines).withLimits(o.ShellDefaultTimeout, o.ShellMaxTimeout, o.ShellWaitDelay),
		newWriteFile(g, o.DocsTask),
		newReadFile(g),
		newListDir(g),
		newSearch(g, files),
		newFindFiles(g, files),
		newEditFile(g, o.DocsTask),
		newProposePlan(o.DocsTask, o.Request),
		newRemember(facts),
	}
	if !o.DisableWeb {
		list = append(list, newWebFetch(cache, o.OnWebFetch))
	}
	list = append(list,
		newRecall(o.Recall),
		newExportDoc(g),
		newOutline(g, files),
		newFinish(),
	)
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

// Without returns a registry that does not offer the named tool.
func (r *Registry) Without(name string) *Registry {
	if r == nil {
		return nil
	}
	var list []Tool
	byName := make(map[string]Tool, len(r.tools))
	for _, t := range r.tools {
		n := t.Spec().Function.Name
		if n == name {
			continue
		}
		list = append(list, t)
		byName[n] = t
	}
	return &Registry{tools: list, byName: byName}
}

func (r *Registry) Named(names ...string) *Registry {
	keep := make(map[string]bool, len(names))
	for _, n := range names {
		keep[n] = true
	}
	var list []Tool
	byName := make(map[string]Tool)
	for _, t := range r.tools {
		n := t.Spec().Function.Name
		if keep[n] {
			list = append(list, t)
			byName[n] = t
		}
	}
	return &Registry{tools: list, byName: byName}
}
