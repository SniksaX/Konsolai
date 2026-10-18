package welcome

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/siraj/konsolai/internal/config"
)

// removedDefaults are the welcome and repl constants that used to repeat
// config.Builtin. The scan covers every non-test Go file in those packages,
// including the helper that copies Builtin values.
var removedDefaults = []struct {
	name string
	pkg  string // empty means both welcome and repl
}{
	{name: "gatherBudget"},
	{name: "probeTimeout"},
	{name: "gitQueryLimit"},
	{name: "memoryQueryLimit"},
	{name: "rateWindow"},
	{name: "welcomeGatherLimit"},
	{name: "welcomeReadyLimit"},
	{name: "welcomeFreshFor"},
	{name: "norm", pkg: "welcome"},
}

func TestRemovedDefaultsComeFromBuiltin(t *testing.T) {
	b := config.Builtin()
	got := ResolveLimits(Limits{})
	if got.TinyHeight != b.LayoutTinyHeight() || got.RecentHeight != b.LayoutRecentHeight() ||
		got.TinyWidth != b.LayoutTinyWidth() || got.NarrowWidth != b.LayoutNarrowWidth() ||
		got.FrameWidth != b.LayoutFrameWidth() {
		t.Fatalf("zero limits %+v, builtin layout %+v", got, b.UI.Layout)
	}

	forbiddenDur := map[time.Duration]string{
		400 * time.Millisecond: "gatherBudget/welcomeGatherLimit",
		300 * time.Millisecond: "probeTimeout",
		200 * time.Millisecond: "gitQueryLimit/memoryQueryLimit",
		500 * time.Millisecond: "welcomeReadyLimit",
		60 * time.Second:       "welcomeFreshFor",
	}
	// Layout cuts and the rate window. 20 is welcome-only: repl still uses 20
	// as a footer width floor, which is not the rate window.
	welcomeInts := map[string]string{
		"20":  "rateWindow",
		"22":  "TinyHeight",
		"30":  "RecentHeight",
		"64":  "TinyWidth",
		"110": "NarrowWidth",
		"120": "FrameWidth",
	}
	layoutInts := map[string]string{
		"22":  "TinyHeight",
		"30":  "RecentHeight",
		"64":  "TinyWidth",
		"110": "NarrowWidth",
		"120": "FrameWidth",
	}

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	roots := []struct {
		pkg string
		dir string
	}{
		{"welcome", filepath.Dir(file)},
		{"repl", filepath.Join(filepath.Dir(file), "..", "repl")},
	}
	fset := token.NewFileSet()
	for _, root := range roots {
		entries, err := os.ReadDir(root.dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, ent := range entries {
			name := ent.Name()
			if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(root.dir, name)
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.Ident:
					for _, rem := range removedDefaults {
						if rem.pkg != "" && rem.pkg != root.pkg {
							continue
						}
						if x.Name == rem.name {
							t.Errorf("%s: removed name %s", fset.Position(x.Pos()), rem.name)
						}
					}
				case *ast.BasicLit:
					if x.Kind != token.INT || root.pkg != "welcome" {
						if x.Kind == token.INT && root.pkg == "repl" {
							if why, ok := layoutInts[x.Value]; ok {
								t.Errorf("%s: literal %s repeats %s", fset.Position(x.Pos()), x.Value, why)
							}
						}
						break
					}
					if why, ok := welcomeInts[x.Value]; ok {
						t.Errorf("%s: literal %s repeats %s", fset.Position(x.Pos()), x.Value, why)
					}
				default:
					if d, ok := durationLiteral(n); ok {
						if why, hit := forbiddenDur[d]; hit {
							t.Errorf("%s: duration %s repeats %s", fset.Position(n.Pos()), d, why)
						}
					}
				}
				return true
			})
		}
	}
}

func durationLiteral(n ast.Node) (time.Duration, bool) {
	be, ok := n.(*ast.BinaryExpr)
	if !ok || be.Op != token.MUL {
		return 0, false
	}
	lit, unit, ok := durationMul(be.X, be.Y)
	if !ok {
		lit, unit, ok = durationMul(be.Y, be.X)
	}
	if !ok {
		return 0, false
	}
	v, err := strconv.Atoi(lit.Value)
	if err != nil {
		return 0, false
	}
	switch unit {
	case "Millisecond":
		return time.Duration(v) * time.Millisecond, true
	case "Second":
		return time.Duration(v) * time.Second, true
	default:
		return 0, false
	}
}

func durationMul(num, unit ast.Expr) (*ast.BasicLit, string, bool) {
	lit, ok := num.(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return nil, "", false
	}
	sel, ok := unit.(*ast.SelectorExpr)
	if !ok {
		return nil, "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "time" {
		return nil, "", false
	}
	return lit, sel.Sel.Name, true
}
