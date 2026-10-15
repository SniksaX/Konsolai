package ui

import (
	"bytes"
	"strings"
	"testing"
)

const sampleMD = `# Title
## Section
- item with ` + "`code`" + ` and **bold**
1. first
> quote
See [docs](http://x)

` + "```python\nprint(1)\n```" + `

| a | b |
| --- | --- |
| 1 | 2 |
`

func TestMarkdownStructureNoColor(t *testing.T) {
	restore := ForceTheme(Palette{})
	defer restore()
	var buf bytes.Buffer
	r := NewRenderer(&buf, false)
	if _, err := r.Write([]byte(sampleMD)); err != nil {
		t.Fatal(err)
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{"Title", "▍ Section", "• item", "code", "bold", "1. first", "│ quote", "docs", "http://x", "─ python", "  print(1)", "│ 1 │ 2 │"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "`code`") {
		t.Fatal("backticks remain")
	}
	if strings.Contains(got, "**bold**") {
		t.Fatal("stars remain")
	}
}

func TestMarkdownColorSGR(t *testing.T) {
	restore := ForceTheme(Palette{Enabled: true, Truecolor: false})
	defer restore()
	var buf bytes.Buffer
	r := NewRenderer(&buf, false)
	_, _ = r.Write([]byte("## Head\n- hi\n`x`\n"))
	_ = r.Flush()
	got := buf.String()
	if !strings.Contains(got, "\033[") {
		t.Fatalf("want SGR: %q", got)
	}
	if !strings.Contains(got, "38;5;141") && !strings.Contains(got, "▍") {
		t.Fatalf("want accent: %q", got)
	}
}

func TestMarkdownChunkedMidTokenFenceBold(t *testing.T) {
	restore := ForceTheme(Palette{})
	defer restore()
	var buf bytes.Buffer
	r := NewRenderer(&buf, false)
	chunks := []string{"**bo", "ld**\n", "```go\n", "func", " main() {}\n", "```\n"}
	for _, c := range chunks {
		if _, err := r.Write([]byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "bold") || strings.Contains(got, "**") {
		t.Fatalf("bold %q", got)
	}
	if !strings.Contains(got, "─ go") || !strings.Contains(got, "func main()") {
		t.Fatalf("fence %q", got)
	}
}

func TestMarkdownUnterminatedFenceFlush(t *testing.T) {
	restore := ForceTheme(Palette{})
	defer restore()
	var buf bytes.Buffer
	r := NewRenderer(&buf, false)
	_, _ = r.Write([]byte("```js\nconst x = 1"))
	_ = r.Flush()
	if !strings.Contains(buf.String(), "const x = 1") {
		t.Fatalf("%q", buf.String())
	}
}

func TestMarkdownPlainPassthrough(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf, true)
	_, _ = r.Write([]byte("# Hi\n"))
	_ = r.Flush()
	if buf.String() != "# Hi\n" {
		t.Fatalf("%q", buf.String())
	}
}

func TestThemeNOCOLOR(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("COLORTERM", "truecolor")
	restore := ForceTheme(detectPalette())
	defer restore()
	p := Current()
	if p.Enabled {
		t.Fatal("NO_COLOR should disable")
	}
	if p.Accent("x") != "x" {
		t.Fatal(p.Accent("x"))
	}
}
