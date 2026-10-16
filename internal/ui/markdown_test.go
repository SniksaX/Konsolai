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

func TestMarkdownCollapsesBlankRuns(t *testing.T) {
	restore := ForceTheme(Palette{})
	defer restore()
	var buf bytes.Buffer
	r := NewRenderer(&buf, false)
	_, _ = r.Write([]byte("a\n\n\n\nb\n"))
	_ = r.Flush()
	if strings.Contains(buf.String(), "a\n\n\nb") {
		t.Fatalf("kept extra blanks: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "a\n\nb") {
		t.Fatalf("want one blank: %q", buf.String())
	}
}

func TestCollapseBlankLinesKeepsFences(t *testing.T) {
	in := "a\n\n\nb\n```\n\n\nx\n```\n"
	got := CollapseBlankLines(in)
	if strings.Count(got, "\n\n\n") != 1 {
		t.Fatalf("fence blanks: %q", got)
	}
	if !strings.Contains(got, "a\n\nb\n") {
		t.Fatalf("prose: %q", got)
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

func TestNestedFourBacktickFence(t *testing.T) {
	restore := ForceTheme(Palette{})
	defer restore()
	var buf bytes.Buffer
	r := NewRenderer(&buf, false)
	src := "````md\n```\ninner\n```\n````\n"
	_, _ = r.Write([]byte(src))
	_ = r.Flush()
	got := buf.String()
	if strings.Count(got, "─ md") != 1 {
		t.Fatalf("want one fence: %q", got)
	}
	if strings.Contains(got, "─ code") || strings.Contains(got, "─ inner") {
		t.Fatalf("inner fence opened a block: %q", got)
	}
	if !strings.Contains(got, "```") || !strings.Contains(got, "inner") {
		t.Fatalf("inner fences should be literal: %q", got)
	}
}

func TestTildeFence(t *testing.T) {
	restore := ForceTheme(Palette{})
	defer restore()
	var buf bytes.Buffer
	r := NewRenderer(&buf, false)
	_, _ = r.Write([]byte("~~~go\nfmt.Println(1)\n~~~\n"))
	_ = r.Flush()
	got := buf.String()
	if !strings.Contains(got, "─ go") || !strings.Contains(got, "fmt.Println(1)") {
		t.Fatalf("%q", got)
	}
}

func TestClosingFenceWithInfoDoesNotClose(t *testing.T) {
	restore := ForceTheme(Palette{})
	defer restore()
	var buf bytes.Buffer
	r := NewRenderer(&buf, false)
	_, _ = r.Write([]byte("```\nhello\n```go\nworld\n```\n"))
	_ = r.Flush()
	got := buf.String()
	if strings.Count(got, "─ code") != 1 {
		t.Fatalf("want one block: %q", got)
	}
	if !strings.Contains(got, "hello") || !strings.Contains(got, "```go") || !strings.Contains(got, "world") {
		t.Fatalf("info-string closer must stay content: %q", got)
	}
}

func TestLenientShorterCloserOnFlush(t *testing.T) {
	restore := ForceTheme(Palette{})
	defer restore()
	var buf bytes.Buffer
	r := NewRenderer(&buf, false)
	src := "````bash\nfind . -type f\n```"
	_, _ = r.Write([]byte(src))
	_ = r.Flush()
	got := buf.String()
	if !strings.Contains(got, "find . -type f") {
		t.Fatalf("body %q", got)
	}
	if strings.Contains(got, "```") {
		t.Fatalf("short closer must not print: %q", got)
	}
	if strings.Count(got, "─ bash") != 1 {
		t.Fatalf("want one fence: %q", got)
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
