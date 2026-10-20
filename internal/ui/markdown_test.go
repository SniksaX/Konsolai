package ui

import (
	"bytes"
	"strconv"
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
	for _, want := range []string{"Title", "Section", "• item", "code", "bold", "1. first", "│ quote", "docs", "http://x", "╭─ python", "print(1)", "│ 1 │ 2 │"} {
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
	restore := ForceTheme(Palette{Enabled: true, Truecolor: false, Scheme: "violet"})
	defer restore()
	var buf bytes.Buffer
	r := NewRenderer(&buf, false)
	_, _ = r.Write([]byte("## Head\n- hi\n`x`\n"))
	_ = r.Flush()
	got := buf.String()
	if !strings.Contains(got, "\033[") {
		t.Fatalf("want SGR: %q", got)
	}
	if !strings.Contains(got, "\033[") && !strings.Contains(got, "\x1b[") && !strings.Contains(got, "▍") {
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

func TestStripEmojiOutsideFences(t *testing.T) {
	defer SetStripEmoji(true)()
	restore := ForceTheme(Palette{})
	defer restore()
	var buf bytes.Buffer
	r := NewRenderer(&buf, true)
	src := "hello 😀 world\n```\nkeep 🎉\n```\nbye ✨\n"
	_, _ = r.Write([]byte(src))
	_ = r.Flush()
	got := buf.String()
	if strings.Contains(got, "😀") || strings.Contains(got, "✨") {
		t.Fatalf("emoji leaked outside fence: %q", got)
	}
	if !strings.Contains(got, "keep 🎉") {
		t.Fatalf("fence should keep emoji: %q", got)
	}
}

func TestFenceFlushesEvery20Lines(t *testing.T) {
	restore := ForceTheme(Palette{})
	defer restore()
	var buf bytes.Buffer
	r := NewRenderer(&buf, false)
	if _, err := r.Write([]byte("```go\n")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < fenceFlushLines-1; i++ {
		if _, err := r.Write([]byte("line " + strconv.Itoa(i) + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(buf.String(), "line 0") {
		t.Fatalf("fence flushed before %d lines: %q", fenceFlushLines, buf.String())
	}
	if _, err := r.Write([]byte("line " + strconv.Itoa(fenceFlushLines-1) + "\n")); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "line 0") || !strings.Contains(got, "line "+strconv.Itoa(fenceFlushLines-1)) {
		t.Fatalf("want flush at %d lines, got %q", fenceFlushLines, got)
	}
	if strings.Contains(got, "╰") {
		t.Fatalf("fence closed early: %q", got)
	}
	if _, err := r.Write([]byte("after\n```\n")); err != nil {
		t.Fatal(err)
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	got = buf.String()
	if !strings.Contains(got, "after") || !strings.Contains(got, "╰") {
		t.Fatalf("closer: %q", got)
	}
}

func TestBlankLookaheadHoldsOneLine(t *testing.T) {
	restore := ForceTheme(Palette{})
	defer restore()
	var buf bytes.Buffer
	r := NewRenderer(&buf, false)
	if _, err := r.Write([]byte("a\n\n")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "\n\n") {
		t.Fatalf("blank emitted before lookahead: %q", buf.String())
	}
	if _, err := r.Write([]byte("\nb\n")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "a\n\nb") {
		t.Fatalf("want one blank: %q", buf.String())
	}
	if strings.Contains(buf.String(), "a\n\n\nb") {
		t.Fatalf("collapsed run: %q", buf.String())
	}
}

func TestTableEscapedPipesKeepColumns(t *testing.T) {
	restore := ForceTheme(Palette{})
	defer restore()
	src := "| Form | Meaning |\n" +
		"| --- | --- |\n" +
		"| `str \\| None` | optional |\n" +
		"| `int \\| str` | either |\n" +
		"\n" +
		"After the table.\n"
	var buf bytes.Buffer
	r := NewRenderer(&buf, false)
	if _, err := r.Write([]byte(src)); err != nil {
		t.Fatal(err)
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "str | None") || !strings.Contains(got, "int | str") {
		t.Fatalf("literal pipes: %q", got)
	}
	if strings.Contains(got, `\|`) {
		t.Fatalf("escape left in output: %q", got)
	}
	headerN := 0
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "Form") {
			headerN = strings.Count(line, "│")
		}
	}
	if headerN < 3 {
		t.Fatalf("header %d\n%s", headerN, got)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "str | None") || strings.Contains(line, "int | str") {
			if strings.Count(line, "│") != headerN {
				t.Fatalf("column count %q\n%s", line, got)
			}
		}
	}
	if !strings.Contains(got, "│\n\nAfter the table.") {
		t.Fatalf("blank line before paragraph: %q", got)
	}
	if strings.Contains(got, "│\n\n\nAfter the table.") {
		t.Fatalf("extra blank: %q", got)
	}
}

func TestTableFitsHeaderColumnCount(t *testing.T) {
	restore := ForceTheme(Palette{})
	defer restore()
	src := "| A | B |\n| --- | --- |\n| only |\n| a | b | c |\n"
	var buf bytes.Buffer
	r := NewRenderer(&buf, false)
	if _, err := r.Write([]byte(src)); err != nil {
		t.Fatal(err)
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	headerN := 0
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "A") && strings.Contains(line, "B") {
			headerN = strings.Count(line, "│")
		}
	}
	if headerN != 3 {
		t.Fatalf("header %d\n%s", headerN, got)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.TrimSpace(line) == "" || !strings.Contains(line, "│") || strings.Contains(line, "┼") {
			continue
		}
		if strings.Count(line, "│") != headerN {
			t.Fatalf("columns %q\n%s", line, got)
		}
	}
	if !strings.Contains(got, "b | c") {
		t.Fatalf("merged cell: %q", got)
	}
}

func TestThemeNOCOLOR(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("COLORTERM", "truecolor")
	ApplyUI("amber", "dark", false, true)
	p := Current()
	if p.Enabled {
		t.Fatal("NO_COLOR should disable")
	}
	if p.Accent("x") != "x" {
		t.Fatal(p.Accent("x"))
	}
}
