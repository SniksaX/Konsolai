package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/safety"
)

const outlineCap = 24 << 10

type outlineTool struct {
	guard *safety.Guard
	files FileLister
}

func newOutline(g *safety.Guard, files FileLister) *outlineTool {
	return &outlineTool{guard: g, files: files}
}

func (t *outlineTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name:        "outline",
			Description: "Outline the structure of a Go, TypeScript, JavaScript or Python file or directory (non-recursive). Returns declarations with line numbers.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"File or directory path relative to the current directory."}},"required":["path"]}`),
		},
	}
}

type outlineArgs struct {
	Path string `json:"path"`
}

type outlineAction struct {
	guard   *safety.Guard
	files   FileLister
	path    string
	display string
}

func (t *outlineTool) Prepare(rawArgs string) (Action, error) {
	var args outlineArgs
	dec := json.NewDecoder(strings.NewReader(rawArgs))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("tools: decode outline: %w", err)
	}
	if strings.TrimSpace(args.Path) == "" {
		return nil, errors.New("tools: outline: path is required")
	}
	resolved, err := t.guard.ResolveReadDir(args.Path)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(resolved); err == nil && !fi.IsDir() && safety.SensitiveRead(resolved) {
		return nil, safety.ErrSensitiveFile
	}
	return &outlineAction{guard: t.guard, files: t.files, path: resolved, display: args.Path}, nil
}

func (a *outlineAction) Summary() string  { return "outline " + a.display }
func (a *outlineAction) Preview() string  { return "" }
func (a *outlineAction) Kind() ActionKind { return ReadOnly }

func (a *outlineAction) Run(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	fi, err := os.Stat(a.path)
	if err != nil {
		return Result{Output: err.Error(), IsError: true, Meta: err.Error()}, nil
	}
	var files []string
	outside := !a.guard.Contains(a.path)
	if fi.IsDir() && outside {
		ents, err := os.ReadDir(a.path)
		if err != nil {
			return Result{Output: err.Error(), IsError: true}, nil
		}
		var b strings.Builder
		decls := 0
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			abs := filepath.Join(a.path, e.Name())
			if safety.SensitiveRead(abs) {
				continue
			}
			text, n := outlineFile(abs, e.Name())
			decls += n
			if text != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(text)
			}
			if b.Len() >= outlineCap {
				break
			}
		}
		out := b.String()
		if out == "" {
			out = "(no declarations)"
		}
		return Result{Output: out, Meta: fmt.Sprintf("%d decls", decls)}, nil
	}
	if !fi.IsDir() && outside {
		if safety.SensitiveRead(a.path) {
			return Result{Output: safety.ErrSensitiveFile.Error(), IsError: true}, nil
		}
		text, n := outlineFile(a.path, a.display)
		out := text
		if out == "" {
			out = "(no declarations)"
		}
		return Result{Output: out, Meta: fmt.Sprintf("%d decls", n)}, nil
	}
	if fi.IsDir() {
		listed, err := a.files(ctx)
		if err != nil {
			return Result{Output: err.Error(), IsError: true, Meta: err.Error()}, nil
		}
		prefix, err := relPrefix(a.guard, a.display)
		if err != nil {
			return Result{Output: err.Error(), IsError: true}, nil
		}
		for _, rel := range listed {
			if prefix == "" {
				if !strings.Contains(rel, "/") {
					files = append(files, rel)
				}
				continue
			}
			rest, ok := strings.CutPrefix(rel, prefix+"/")
			if ok && !strings.Contains(rest, "/") {
				files = append(files, rel)
			}
		}
	} else {
		rel, err := filepath.Rel(a.guard.Root(), a.path)
		if err != nil {
			return Result{Output: err.Error(), IsError: true}, nil
		}
		files = []string{filepath.ToSlash(rel)}
	}
	var b strings.Builder
	decls := 0
	for _, rel := range files {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		abs, err := a.guard.ResolveFromRoot(rel)
		if err != nil {
			continue
		}
		if safety.SensitiveRead(abs) {
			continue
		}
		text, n := outlineFile(abs, rel)
		decls += n
		if text != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(text)
		}
		if b.Len() >= outlineCap {
			break
		}
	}
	out := b.String()
	if len(out) > outlineCap {
		out = out[:outlineCap] + "\n... [truncated]"
	}
	if out == "" {
		out = "(no declarations)"
	}
	return Result{Output: out, Meta: fmt.Sprintf("%d decls", decls)}, nil
}

func FileOutline(abs, rel string) string {
	s, _ := outlineFile(abs, rel)
	return s
}

func outlineFile(abs, rel string) (string, int) {
	ext := strings.ToLower(filepath.Ext(abs))
	switch ext {
	case ".go":
		return outlineGo(abs, rel)
	case ".ts", ".tsx", ".js", ".mjs", ".py":
		return outlineLines(abs, rel)
	default:
		return "", 0
	}
}

func outlineGo(abs, rel string) (string, int) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, abs, nil, parser.ParseComments)
	if err != nil {
		return fmt.Sprintf("%s: parse error: %s", rel, err), 0
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\npackage %s\n", rel, f.Name.Name)
	n := 0
	for _, imp := range f.Imports {
		path := imp.Path.Value
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name + " "
		}
		pos := fset.Position(imp.Pos()).Line
		fmt.Fprintf(&b, "L%d import %s%s\n", pos, name, path)
		n++
	}
	for _, d := range f.Decls {
		switch x := d.(type) {
		case *ast.FuncDecl:
			n++
			recv := ""
			if x.Recv != nil && len(x.Recv.List) > 0 {
				recv = "(" + exprString(x.Recv.List[0].Type) + ") "
			}
			fmt.Fprintf(&b, "L%d func %s%s%s\n", fset.Position(x.Pos()).Line, recv, x.Name.Name, fieldListSig(x.Type))
		case *ast.GenDecl:
			for _, spec := range x.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					n++
					fmt.Fprintf(&b, "L%d type %s\n", fset.Position(s.Pos()).Line, s.Name.Name)
					if st, ok := s.Type.(*ast.StructType); ok && st.Fields != nil {
						for _, f := range st.Fields.List {
							names := fieldNames(f)
							fmt.Fprintf(&b, "  field %s %s\n", names, exprString(f.Type))
						}
					}
					if it, ok := s.Type.(*ast.InterfaceType); ok && it.Methods != nil {
						for _, m := range it.Methods.List {
							fmt.Fprintf(&b, "  method %s\n", fieldNames(m))
						}
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						n++
						kind := "var"
						if x.Tok == token.CONST {
							kind = "const"
						}
						fmt.Fprintf(&b, "L%d %s %s\n", fset.Position(name.Pos()).Line, kind, name.Name)
					}
				}
			}
		}
	}
	return strings.TrimRight(b.String(), "\n"), n
}

func fieldNames(f *ast.Field) string {
	if len(f.Names) == 0 {
		return exprString(f.Type)
	}
	var names []string
	for _, n := range f.Names {
		names = append(names, n.Name)
	}
	return strings.Join(names, ", ")
}

func fieldListSig(t *ast.FuncType) string {
	var b strings.Builder
	b.WriteByte('(')
	if t.Params != nil {
		for i, f := range t.Params.List {
			if i > 0 {
				b.WriteString(", ")
			}
			if len(f.Names) > 0 {
				b.WriteString(fieldNames(f))
				b.WriteByte(' ')
			}
			b.WriteString(exprString(f.Type))
		}
	}
	b.WriteByte(')')
	if t.Results != nil && len(t.Results.List) > 0 {
		b.WriteByte(' ')
		needParen := len(t.Results.List) > 1 || (len(t.Results.List) == 1 && len(t.Results.List[0].Names) > 0)
		if needParen {
			b.WriteByte('(')
		}
		for i, f := range t.Results.List {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(exprString(f.Type))
		}
		if needParen {
			b.WriteByte(')')
		}
	}
	return b.String()
}

func exprString(e ast.Expr) string {
	return compactExpr(e)
}

func compactExpr(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.StarExpr:
		return "*" + compactExpr(x.X)
	case *ast.SelectorExpr:
		return compactExpr(x.X) + "." + x.Sel.Name
	case *ast.ArrayType:
		return "[]" + compactExpr(x.Elt)
	case *ast.Ellipsis:
		return "..." + compactExpr(x.Elt)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.MapType:
		return "map[" + compactExpr(x.Key) + "]" + compactExpr(x.Value)
	case *ast.ChanType:
		return "chan " + compactExpr(x.Value)
	case *ast.FuncType:
		return "func" + fieldListSig(x)
	default:
		return fmt.Sprintf("%T", e)
	}
}

var lineDecl = regexp.MustCompile(`(?i)^\s*(export\s+)?(async\s+)?(default\s+)?((function|class|interface|type|def)\b|const\s+\w+|let\s+\w+|var\s+\w+)`)

func outlineLines(abs, rel string) (string, int) {
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", 0
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%s\n", rel)
	n := 0
	for i, line := range strings.Split(string(b), "\n") {
		if lineDecl.MatchString(line) {
			n++
			fmt.Fprintf(&out, "L%d %s\n", i+1, strings.TrimSpace(line))
		}
	}
	return strings.TrimRight(out.String(), "\n"), n
}
