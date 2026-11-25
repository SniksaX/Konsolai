package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/SniksaX/Konsolai/internal/ui/theme"
	"golang.org/x/term"
)

// AnswerPanel is the final-answer frame. The body is markdown rendered with
// the normal renderer, with pretty JSON blocks and a trailing command list.
func AnswerPanel(title, markdown string, width, jsonMaxLines int) string {
	if width < 20 {
		width = 20
	}
	lines := answerPanelBody(markdown, width, jsonMaxLines)
	return drawAnswerPanel(title, lines, width)
}

func answerPanelBody(markdown string, width, jsonMax int) []string {
	inner := panelInner(width)
	if strings.TrimSpace(markdown) == "" {
		return nil
	}
	if pretty, ok := prettyJSONLines(markdown, jsonMax); ok {
		return pretty
	}
	head, commands := splitRunYourself(markdown)
	var out []string
	for _, chunk := range splitJSONFences(head) {
		if chunk.json {
			if pretty, ok := prettyJSONLines(chunk.text, jsonMax); ok {
				out = append(out, pretty...)
			} else {
				out = append(out, renderMarkdownLines("```json\n"+chunk.text+"\n```")...)
			}
			continue
		}
		out = append(out, renderMarkdownLines(accentPaths(chunk.text))...)
	}
	if commands != "" {
		out = append(out, renderMarkdownLines("```sh\n"+commands+"\n```")...)
	}
	return wrapLines(out, inner)
}

type panelChunk struct {
	text string
	json bool
}

var jsonFence = regexp.MustCompile("(?ms)^```json[ \t]*\n(.*?)\n```[ \t]*$")

func splitJSONFences(text string) []panelChunk {
	var out []panelChunk
	for {
		loc := jsonFence.FindStringSubmatchIndex(text)
		if loc == nil {
			if strings.TrimSpace(text) != "" {
				out = append(out, panelChunk{text: text})
			}
			return out
		}
		if before := text[:loc[0]]; strings.TrimSpace(before) != "" {
			out = append(out, panelChunk{text: before})
		}
		out = append(out, panelChunk{text: text[loc[2]:loc[3]], json: true})
		text = text[loc[1]:]
	}
}

var runYourself = regexp.MustCompile(`(?mi)^[ \t]*(?:run (?:it|these|them|the commands) yourself|run these yourself)[ \t]*:?[ \t]*\n`)

func splitRunYourself(text string) (head, commands string) {
	loc := runYourself.FindStringIndex(text)
	if loc == nil {
		return text, ""
	}
	// Keep the "Run it yourself:" line in the prose so the reader still sees it.
	head = text[:loc[1]]
	commands = strings.TrimSpace(text[loc[1]:])
	commands = strings.TrimPrefix(commands, "```sh")
	commands = strings.TrimPrefix(commands, "```bash")
	commands = strings.TrimSuffix(strings.TrimSpace(commands), "```")
	return head, strings.TrimSpace(commands)
}

var pathToken = regexp.MustCompile(`\b[\w./~-]+\.(?:go|md|ya?ml|json|txt|sh|py|ts|tsx|js|jsx|css|html|toml|mod|sum)\b`)

func accentPaths(text string) string {
	return pathToken.ReplaceAllStringFunc(text, func(s string) string {
		return "`" + s + "`"
	})
}

func renderMarkdownLines(md string) []string {
	var b bytes.Buffer
	r := NewRenderer(&b, false)
	_, _ = r.Write([]byte(md))
	_ = r.Flush()
	text := strings.TrimRight(b.String(), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// PrettyJSON returns the jq-style rendering of text when it is valid JSON.
func PrettyJSON(text string, maxLines int) (string, bool) {
	lines, ok := prettyJSONLines(text, maxLines)
	if !ok {
		return "", false
	}
	return strings.Join(lines, "\n"), true
}

func prettyJSONLines(raw string, maxLines int) ([]string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	if dec.More() {
		return nil, false
	}
	var out []string
	lines := colorJSON(v, 0)
	out = append(out, lines...)
	if maxLines > 0 && len(out) > maxLines {
		extra := len(out) - (maxLines - 1)
		out = out[:maxLines-1]
		out = append(out, theme.Style(theme.Muted, fmt.Sprintf("... %d more lines", extra)))
	}
	return out, true
}

func colorJSON(v interface{}, depth int) []string {
	pad := strings.Repeat("  ", depth)
	switch val := v.(type) {
	case map[string]interface{}:
		if len(val) == 0 {
			return []string{theme.Style(theme.Subtle, "{}")}
		}
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sortStrings(keys)
		out := []string{theme.Style(theme.Subtle, "{")}
		for i, k := range keys {
			child := colorJSON(val[k], depth+1)
			child[0] = pad + "  " + theme.Style(theme.Link, strconv.Quote(k)) + theme.Style(theme.Subtle, ": ") + strings.TrimPrefix(child[0], pad+"  ")
			if i < len(keys)-1 {
				child[len(child)-1] += theme.Style(theme.Subtle, ",")
			}
			out = append(out, child...)
		}
		out = append(out, pad+theme.Style(theme.Subtle, "}"))
		return out
	case []interface{}:
		if len(val) == 0 {
			return []string{theme.Style(theme.Subtle, "[]")}
		}
		out := []string{theme.Style(theme.Subtle, "[")}
		for i, item := range val {
			child := colorJSON(item, depth+1)
			child[0] = pad + "  " + strings.TrimPrefix(child[0], pad+"  ")
			if i < len(val)-1 {
				child[len(child)-1] += theme.Style(theme.Subtle, ",")
			}
			out = append(out, child...)
		}
		out = append(out, pad+theme.Style(theme.Subtle, "]"))
		return out
	case string:
		return []string{theme.Style(theme.Success, strconv.Quote(val))}
	case json.Number:
		return []string{theme.Style(theme.Accent, val.String())}
	case bool:
		return []string{theme.Style(theme.Warn, strconv.FormatBool(val))}
	case nil:
		return []string{theme.Style(theme.Muted, "null")}
	default:
		return []string{theme.Style(theme.Muted, fmt.Sprint(val))}
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func panelInner(width int) int {
	if width < 60 {
		return width - 2
	}
	return width - 4
}

var sgr = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// wrapLines word-wraps rendered ANSI lines and keeps the active style on the
// continuation lines.
func wrapLines(lines []string, width int) []string {
	if width < 1 {
		return lines
	}
	var out []string
	for _, line := range lines {
		out = append(out, wrapLine(line, width)...)
	}
	return out
}

func wrapLine(line string, width int) []string {
	if theme.VisibleWidth(line) <= width {
		return []string{line}
	}
	var out []string
	var cur, active strings.Builder
	col := 0
	flush := func() {
		out = append(out, cur.String())
		cur.Reset()
		cur.WriteString(active.String())
		col = theme.VisibleWidth(active.String())
	}
	for i := 0; i < len(line); {
		if loc := sgr.FindStringIndex(line[i:]); loc != nil && loc[0] == 0 {
			seq := line[i : i+loc[1]]
			cur.WriteString(seq)
			if seq == "\x1b[0m" {
				active.Reset()
			} else {
				active.WriteString(seq)
			}
			i += loc[1]
			continue
		}
		if line[i] == ' ' {
			if col >= width {
				flush()
			} else {
				cur.WriteByte(' ')
				col++
			}
			i++
			continue
		}
		j := i
		for j < len(line) && line[j] != ' ' && line[j] != 0x1b {
			j++
		}
		word := line[i:j]
		ww := theme.DisplayWidth(word)
		if col > 0 && col+ww > width {
			flush()
		}
		cur.WriteString(word)
		col += ww
		i = j
	}
	if strings.TrimSpace(theme.StripANSI(cur.String())) != "" {
		out = append(out, cur.String())
	}
	return out
}

func drawAnswerPanel(title string, lines []string, width int) string {
	p := Current()
	inner := panelInner(width)
	var b strings.Builder
	if width < 60 {
		b.WriteString(p.AccentDim("▌") + " " + p.Heading(title) + "\n")
		for _, line := range lines {
			b.WriteString(p.AccentDim("▌") + " " + line + "\n")
		}
		return b.String()
	}
	titleText := p.Heading(title)
	used := 2 + theme.VisibleWidth(titleText) + 2
	fill := width - used
	if fill < 0 {
		fill = 0
	}
	b.WriteString(p.Subtle("╭─ ") + titleText + " " + p.Subtle(strings.Repeat("─", fill)) + p.Subtle("╮") + "\n")
	for _, line := range lines {
		pad := inner - theme.VisibleWidth(line)
		if pad < 0 {
			pad = 0
		}
		b.WriteString(p.Subtle("│") + " " + line + strings.Repeat(" ", pad) + " " + p.Subtle("│") + "\n")
	}
	b.WriteString(p.Subtle("╰"+strings.Repeat("─", width-2)+"╯") + "\n")
	return b.String()
}

// PanelTitle maps a turn exit code to the panel title.
func PanelTitle(code int) string {
	if code == 0 {
		return "Done"
	}
	return "Stopped"
}

// AnswerPanelAllowed reports whether the answer panel may replace streamed
// text: a TTY writer, colors enabled, and enough rows for the panel plus the
// prompt.
func AnswerPanelAllowed(w io.Writer, panelLines int) (width, height int, ok bool) {
	if !theme.Enabled() {
		return 0, 0, false
	}
	f := termFile(w)
	if f == nil {
		return 0, 0, false
	}
	width, height, err := term.GetSize(int(f.Fd()))
	if err != nil || width <= 0 || height <= 0 {
		return 0, 0, false
	}
	if panelLines > 0 && height < panelLines+3 {
		return 0, 0, false
	}
	return width, height, true
}

func termFile(w io.Writer) *os.File {
	for i := 0; i < 6 && w != nil; i++ {
		if f, ok := w.(*os.File); ok {
			return f
		}
		u, ok := w.(interface{ Unwrap() io.Writer })
		if !ok {
			return nil
		}
		next := u.Unwrap()
		if next == w {
			return nil
		}
		w = next
	}
	return nil
}

// RowCounter counts the visual rows written through it, for replacing the
// streamed answer with the panel.
type RowCounter struct {
	w     io.Writer
	width int
	rows  int
	col   int
}

func NewRowCounter(w io.Writer, width int) *RowCounter {
	if width < 1 {
		width = theme.TermWidth()
	}
	return &RowCounter{w: w, width: width, rows: 1}
}

func (c *RowCounter) Write(p []byte) (int, error) {
	for i := 0; i < len(p); {
		if loc := sgr.FindStringIndex(string(p[i:])); loc != nil && loc[0] == 0 {
			i += loc[1]
			continue
		}
		if p[i] == '\n' {
			c.rows++
			c.col = 0
			i++
			continue
		}
		c.col++
		if c.col >= c.width {
			c.rows++
			c.col = 0
		}
		i++
	}
	return c.w.Write(p)
}

// Rows is the number of visual rows written so far.
func (c *RowCounter) Rows() int {
	if c == nil {
		return 0
	}
	return c.rows
}
