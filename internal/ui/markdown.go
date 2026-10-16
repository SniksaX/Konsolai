package ui

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

var (
	reHeading = regexp.MustCompile(`^(#{1,3})\s+(.*)$`)
	reBullet  = regexp.MustCompile(`^(\s*)([-*+])\s+(.*)$`)
	reNumber  = regexp.MustCompile(`^(\s*)(\d+)([.)])\s+(.*)$`)
	reQuote   = regexp.MustCompile(`^(\s*)>\s?(.*)$`)
	reTable   = regexp.MustCompile(`^\s*\|`)
)

// Renderer is a streaming markdown writer that flushes on newlines.
type Renderer struct {
	w         io.Writer
	plain     bool
	buf       []byte
	inFence   bool
	fenceChar byte
	fenceN    int
	fenceLang string
	fenceBody strings.Builder
	table     []string
	lastBlank bool
}

func NewRenderer(w io.Writer, plain bool) *Renderer {
	return &Renderer{w: w, plain: plain}
}

func (r *Renderer) Write(p []byte) (int, error) {
	n := len(p)
	r.buf = append(r.buf, p...)
	for {
		i := bytes.IndexByte(r.buf, '\n')
		if i < 0 {
			return n, nil
		}
		line := string(r.buf[:i])
		r.buf = r.buf[i+1:]
		if err := r.line(line); err != nil {
			return n, err
		}
	}
}

func (r *Renderer) Flush() error {
	if r.inFence {
		body := r.fenceBody.String()
		if len(r.buf) > 0 {
			body += string(r.buf)
			r.buf = nil
		}
		body = stripLenientCloser(body, r.fenceChar, r.fenceN)
		if err := r.emitFence(r.fenceLang, body); err != nil {
			return err
		}
		r.inFence = false
		r.fenceBody.Reset()
	}
	if err := r.flushTable(); err != nil {
		return err
	}
	if len(r.buf) > 0 {
		if err := r.line(string(r.buf)); err != nil {
			return err
		}
		r.buf = nil
	}
	return nil
}

func (r *Renderer) line(s string) error {
	if r.plain {
		return r.writeCollapsed(s, s+"\n")
	}
	if r.inFence {
		if isCloseFence(strings.TrimRight(s, "\r"), r.fenceChar, r.fenceN) {
			body := r.fenceBody.String()
			r.fenceBody.Reset()
			r.inFence = false
			return r.emitFence(r.fenceLang, body)
		}
		r.fenceBody.WriteString(s)
		r.fenceBody.WriteByte('\n')
		return nil
	}
	if char, n, lang, ok := parseOpenFence(strings.TrimRight(s, "\r")); ok {
		if err := r.flushTable(); err != nil {
			return err
		}
		r.inFence = true
		r.fenceChar = char
		r.fenceN = n
		r.fenceLang = lang
		r.lastBlank = false
		return nil
	}
	if reTable.MatchString(s) {
		r.table = append(r.table, s)
		return nil
	}
	if err := r.flushTable(); err != nil {
		return err
	}
	return r.emitLine(s)
}

func parseOpenFence(s string) (char byte, n int, lang string, ok bool) {
	i := 0
	for i < len(s) && i < 3 && s[i] == ' ' {
		i++
	}
	if i >= len(s) {
		return 0, 0, "", false
	}
	char = s[i]
	if char != '`' && char != '~' {
		return 0, 0, "", false
	}
	start := i
	for i < len(s) && s[i] == char {
		i++
	}
	n = i - start
	if n < 3 {
		return 0, 0, "", false
	}
	info := strings.TrimSpace(s[i:])
	if char == '`' && strings.Contains(info, "`") {
		return 0, 0, "", false
	}
	lang = info
	if j := strings.IndexAny(info, " \t"); j >= 0 {
		lang = info[:j]
	}
	return char, n, lang, true
}

func stripLenientCloser(body string, char byte, n int) string {
	if n <= 3 || body == "" {
		return body
	}
	body = strings.TrimRight(body, "\n")
	i := strings.LastIndexByte(body, '\n')
	last := body
	head := ""
	if i >= 0 {
		head = body[:i]
		last = body[i+1:]
	}
	if isLenientClose(last, char, n) {
		return strings.TrimRight(head, "\n")
	}
	return body
}

func isLenientClose(s string, char byte, n int) bool {
	if n <= 3 {
		return false
	}
	s = strings.TrimRight(s, "\r")
	i := 0
	for i < len(s) && i < 3 && s[i] == ' ' {
		i++
	}
	start := i
	for i < len(s) && s[i] == char {
		i++
	}
	count := i - start
	if count < 3 || count >= n {
		return false
	}
	return strings.TrimSpace(s[i:]) == ""
}

func isCloseFence(s string, char byte, n int) bool {
	i := 0
	for i < len(s) && i < 3 && s[i] == ' ' {
		i++
	}
	start := i
	for i < len(s) && s[i] == char {
		i++
	}
	if i-start < n {
		return false
	}
	return strings.TrimSpace(s[i:]) == ""
}

func (r *Renderer) writeCollapsed(s, out string) error {
	if strings.TrimSpace(s) == "" {
		if r.lastBlank {
			return nil
		}
		r.lastBlank = true
		_, err := io.WriteString(r.w, "\n")
		return err
	}
	r.lastBlank = false
	_, err := io.WriteString(r.w, out)
	return err
}

func (r *Renderer) emitLine(s string) error {
	if strings.TrimSpace(s) == "" {
		return r.writeCollapsed(s, "\n")
	}
	r.lastBlank = false
	p := Current()
	switch {
	case reHeading.MatchString(s):
		m := reHeading.FindStringSubmatch(s)
		text := p.Heading(inline(p, m[2]))
		if m[1] != "#" {
			text = p.Accent("▍ ") + text
		}
		_, err := io.WriteString(r.w, text+"\n")
		return err
	case reBullet.MatchString(s):
		m := reBullet.FindStringSubmatch(s)
		_, err := io.WriteString(r.w, m[1]+p.Accent("•")+" "+inline(p, m[3])+"\n")
		return err
	case reNumber.MatchString(s):
		m := reNumber.FindStringSubmatch(s)
		_, err := io.WriteString(r.w, m[1]+p.Accent(m[2]+m[3])+" "+inline(p, m[4])+"\n")
		return err
	case reQuote.MatchString(s):
		m := reQuote.FindStringSubmatch(s)
		_, err := io.WriteString(r.w, m[1]+p.Muted("│ ")+inline(p, m[2])+"\n")
		return err
	default:
		_, err := io.WriteString(r.w, inline(p, s)+"\n")
		return err
	}
}

func (r *Renderer) emitFence(lang, body string) error {
	r.lastBlank = false
	p := Current()
	label := lang
	if label == "" {
		label = "code"
	}
	rule := p.Muted("─ " + label + " " + strings.Repeat("─", 12))
	if _, err := io.WriteString(r.w, rule+"\n"); err != nil {
		return err
	}
	code := strings.TrimRight(body, "\n")
	highlighted := highlight(lang, code, p)
	if highlighted != "" {
		for _, line := range strings.Split(strings.TrimRight(highlighted, "\n"), "\n") {
			if _, err := io.WriteString(r.w, "  "+line+"\n"); err != nil {
				return err
			}
		}
	} else {
		for _, line := range strings.Split(code, "\n") {
			if _, err := io.WriteString(r.w, "  "+p.Code(line)+"\n"); err != nil {
				return err
			}
		}
	}
	_, err := io.WriteString(r.w, p.Muted(strings.Repeat("─", 16))+"\n")
	return err
}

func highlight(lang, code string, p Palette) string {
	if !p.Enabled || code == "" {
		return ""
	}
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)
	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}
	name := "terminal256"
	if p.Truecolor {
		name = "terminal16m"
	}
	fm := formatters.Get(name)
	if fm == nil {
		fm = formatters.Fallback
	}
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return ""
	}
	var b strings.Builder
	if err := fm.Format(&b, style, it); err != nil {
		return ""
	}
	return b.String()
}

func (r *Renderer) flushTable() error {
	if len(r.table) == 0 {
		return nil
	}
	rows := r.table
	r.table = nil
	p := Current()
	parsed := make([][]string, 0, len(rows))
	for _, row := range rows {
		if isTableSep(row) {
			parsed = append(parsed, nil)
			continue
		}
		parsed = append(parsed, splitRow(row))
	}
	widths := []int{}
	for _, cells := range parsed {
		for i, c := range cells {
			for len(widths) <= i {
				widths = append(widths, 0)
			}
			if n := displayLen(c); n > widths[i] {
				widths[i] = n
			}
		}
	}
	for _, cells := range parsed {
		if cells == nil {
			var b strings.Builder
			b.WriteString(p.Muted("│"))
			for i, w := range widths {
				if i > 0 {
					b.WriteString(p.Muted("┼"))
				}
				b.WriteString(p.Muted(strings.Repeat("─", w+2)))
			}
			b.WriteString(p.Muted("│") + "\n")
			if _, err := io.WriteString(r.w, b.String()); err != nil {
				return err
			}
			continue
		}
		var b strings.Builder
		b.WriteString("│")
		for i, w := range widths {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			fmt.Fprintf(&b, " %-*s │", w, cell)
		}
		b.WriteByte('\n')
		if _, err := io.WriteString(r.w, b.String()); err != nil {
			return err
		}
	}
	return nil
}

func splitRow(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "|")
	s = strings.TrimSuffix(s, "|")
	parts := strings.Split(s, "|")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

func isTableSep(s string) bool {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "-") {
		return false
	}
	for _, r := range s {
		if r != '|' && r != '-' && r != ':' && r != ' ' {
			return false
		}
	}
	return true
}

func displayLen(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsControl(r) {
			n++
		}
	}
	return n
}

func inline(p Palette, s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '`' {
			if j := strings.IndexByte(s[i+1:], '`'); j >= 0 {
				b.WriteString(p.Code(s[i+1 : i+1+j]))
				i += j + 2
				continue
			}
		}
		if strings.HasPrefix(s[i:], "[") {
			if text, url, n, ok := parseLink(s[i:]); ok {
				b.WriteString(p.Link(text))
				if url != "" {
					b.WriteString(" ")
					b.WriteString(p.Muted(url))
				}
				i += n
				continue
			}
		}
		if strings.HasPrefix(s[i:], "**") {
			if j := strings.Index(s[i+2:], "**"); j >= 0 {
				b.WriteString(p.Strong(s[i+2 : i+2+j]))
				i += j + 4
				continue
			}
		}
		if s[i] == '*' {
			if j := strings.IndexByte(s[i+1:], '*'); j >= 0 && !strings.Contains(s[i+1:i+1+j], "*") {
				b.WriteString(p.Italic(s[i+1 : i+1+j]))
				i += j + 2
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func parseLink(s string) (text, url string, n int, ok bool) {
	if !strings.HasPrefix(s, "[") {
		return "", "", 0, false
	}
	end := strings.IndexByte(s, ']')
	if end < 0 || end+1 >= len(s) || s[end+1] != '(' {
		return "", "", 0, false
	}
	close := strings.IndexByte(s[end+2:], ')')
	if close < 0 {
		return "", "", 0, false
	}
	return s[1:end], s[end+2 : end+2+close], end + 3 + close, true
}

// CollapseBlankLines reduces runs of blank lines to one, outside fenced code blocks.
func CollapseBlankLines(s string) string {
	if s == "" {
		return s
	}
	ends := strings.HasSuffix(s, "\n")
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	var b strings.Builder
	inFence := false
	var fenceChar byte
	var fenceN int
	lastBlank := false
	for i, line := range lines {
		raw := strings.TrimRight(line, "\r")
		if inFence {
			b.WriteString(line)
			if i < len(lines)-1 || ends {
				b.WriteByte('\n')
			}
			if isCloseFence(raw, fenceChar, fenceN) {
				inFence = false
			}
			lastBlank = false
			continue
		}
		if char, n, _, ok := parseOpenFence(raw); ok {
			inFence = true
			fenceChar, fenceN = char, n
			lastBlank = false
			b.WriteString(line)
			if i < len(lines)-1 || ends {
				b.WriteByte('\n')
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			if lastBlank {
				continue
			}
			lastBlank = true
		} else {
			lastBlank = false
		}
		b.WriteString(line)
		if i < len(lines)-1 || ends {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
