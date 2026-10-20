package ui

import (
	"bytes"
	"io"
	"regexp"
	"strings"

	"github.com/SniksaX/Konsolai/internal/ui/theme"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
)

var (
	reHeading = regexp.MustCompile(`^(#{1,3})\s+(.*)$`)
	reBullet  = regexp.MustCompile(`^(\s*)([-*+])\s+(.*)$`)
	reNumber  = regexp.MustCompile(`^(\s*)(\d+)([.)])\s+(.*)$`)
	reQuote   = regexp.MustCompile(`^(\s*)>\s?(.*)$`)
	reTable   = regexp.MustCompile(`^\s*\|`)
)

// fenceFlushLines is how often an open fence is painted before its closer.
const fenceFlushLines = 20

// Renderer is a streaming markdown writer. Ordinary lines flush at each
// newline. One pending blank is the only lookahead for collapsing blank
// runs. An open fence flushes every fenceFlushLines lines or at its closer.
// A table flushes when it ends.
type Renderer struct {
	w            io.Writer
	plain        bool
	buf          []byte
	inFence      bool
	fenceChar    byte
	fenceN       int
	fenceLang    string
	fenceBody    strings.Builder
	fenceLines   int
	fenceStarted bool
	table        []string
	pendingBlank bool
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
		if len(r.buf) > 0 {
			r.fenceBody.WriteString(string(r.buf))
			r.buf = nil
		}
		body := stripLenientCloser(r.fenceBody.String(), r.fenceChar, r.fenceN)
		r.fenceBody.Reset()
		r.fenceLines = 0
		if r.fenceStarted {
			r.fenceBody.WriteString(body)
			if err := r.finishFence(); err != nil {
				return err
			}
		} else {
			r.inFence = false
			if err := r.emitFence(r.fenceLang, body); err != nil {
				return err
			}
		}
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
	return r.flushPendingBlank()
}

func (r *Renderer) line(s string) error {
	if r.inFence {
		if isCloseFence(strings.TrimRight(s, "\r"), r.fenceChar, r.fenceN) {
			if r.plain {
				r.inFence = false
				r.fenceBody.Reset()
				r.fenceLines = 0
				r.fenceStarted = false
				return r.writeCollapsed(s, s+"\n")
			}
			if r.fenceStarted {
				return r.finishFence()
			}
			body := r.fenceBody.String()
			r.fenceBody.Reset()
			r.fenceLines = 0
			r.inFence = false
			return r.emitFence(r.fenceLang, body)
		}
		r.fenceBody.WriteString(s)
		r.fenceBody.WriteByte('\n')
		r.fenceLines++
		if r.plain {
			return r.writeCollapsed(s, s+"\n")
		}
		if r.fenceLines >= fenceFlushLines {
			return r.spillFence()
		}
		return nil
	}
	if char, n, lang, ok := parseOpenFence(strings.TrimRight(s, "\r")); ok {
		if err := r.flushPendingBlank(); err != nil {
			return err
		}
		if err := r.flushTable(); err != nil {
			return err
		}
		r.inFence = true
		r.fenceChar = char
		r.fenceN = n
		r.fenceLang = lang
		r.pendingBlank = false
		if r.plain {
			return r.writeCollapsed(s, s+"\n")
		}
		return nil
	}
	if StripEmojiOn() {
		s = StripEmoji(s)
	}
	if r.plain {
		return r.writeCollapsed(s, s+"\n")
	}
	if reTable.MatchString(s) {
		if len(r.table) == 0 {
			if err := r.flushPendingBlank(); err != nil {
				return err
			}
		}
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
		if r.pendingBlank {
			return nil
		}
		r.pendingBlank = true
		return nil
	}
	if err := r.flushPendingBlank(); err != nil {
		return err
	}
	_, err := io.WriteString(r.w, out)
	return err
}

func (r *Renderer) flushPendingBlank() error {
	if !r.pendingBlank {
		return nil
	}
	r.pendingBlank = false
	_, err := io.WriteString(r.w, "\n")
	return err
}

func (r *Renderer) emitLine(s string) error {
	if strings.TrimSpace(s) == "" {
		return r.writeCollapsed(s, "\n")
	}
	if err := r.flushPendingBlank(); err != nil {
		return err
	}
	p := Current()
	switch {
	case reHeading.MatchString(s):
		m := reHeading.FindStringSubmatch(s)
		text := p.Heading(inline(p, m[2]))
		_, err := io.WriteString(r.w, text+"\n")
		return err
	case reBullet.MatchString(s):
		m := reBullet.FindStringSubmatch(s)
		return r.writeHanging(m[1], p.AccentDim("•")+" ", m[3])
	case reNumber.MatchString(s):
		m := reNumber.FindStringSubmatch(s)
		return r.writeHanging(m[1], p.AccentDim(m[2]+m[3])+" ", m[4])
	case reQuote.MatchString(s):
		m := reQuote.FindStringSubmatch(s)
		_, err := io.WriteString(r.w, m[1]+p.Subtle("│ ")+inline(p, m[2])+"\n")
		return err
	default:
		if !r.plain {
			wrapped := theme.Wrap(s, theme.WrapWidth())
			var b strings.Builder
			for i, ln := range strings.Split(wrapped, "\n") {
				if i > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(inline(p, ln))
			}
			_, err := io.WriteString(r.w, b.String()+"\n")
			return err
		}
		_, err := io.WriteString(r.w, inline(p, s)+"\n")
		return err
	}
}

func (r *Renderer) writeHanging(indent, marker, text string) error {
	p := Current()
	hang := theme.DisplayWidth(indent + theme.StripANSI(marker))
	width := theme.WrapWidth()
	bodyW := width - hang
	if bodyW < 8 {
		bodyW = 8
	}
	lines := strings.Split(theme.Wrap(text, bodyW), "\n")
	var b strings.Builder
	b.WriteString(indent + marker + inline(p, lines[0]) + "\n")
	pad := strings.Repeat(" ", hang)
	for _, ln := range lines[1:] {
		b.WriteString(pad + inline(p, ln) + "\n")
	}
	_, err := io.WriteString(r.w, b.String())
	return err
}

func (r *Renderer) spillFence() error {
	body := r.fenceBody.String()
	r.fenceBody.Reset()
	r.fenceLines = 0
	p := Current()
	if !r.fenceStarted {
		label := r.fenceLang
		if label == "" {
			label = "code"
		}
		head := p.Subtle("╭─") + " " + p.Muted(label)
		if _, err := io.WriteString(r.w, head+"\n"); err != nil {
			return err
		}
		r.fenceStarted = true
	}
	return r.writeFenceLines(body)
}

func (r *Renderer) finishFence() error {
	if err := r.spillFence(); err != nil {
		return err
	}
	r.inFence = false
	r.fenceStarted = false
	_, err := io.WriteString(r.w, Current().Subtle("╰─")+"\n")
	return err
}

func (r *Renderer) writeFenceLines(body string) error {
	code := strings.TrimRight(body, "\n")
	if code == "" {
		return nil
	}
	p := Current()
	highlighted := highlight(r.fenceLang, code, p)
	var lines []string
	if highlighted != "" {
		lines = strings.Split(strings.TrimRight(highlighted, "\n"), "\n")
	} else {
		lines = strings.Split(code, "\n")
	}
	for _, line := range lines {
		if _, err := io.WriteString(r.w, p.Subtle("│ ")+line+"\n"); err != nil {
			return err
		}
	}
	return nil
}

func (r *Renderer) emitFence(lang, body string) error {
	r.pendingBlank = false
	p := Current()
	label := lang
	if label == "" {
		label = "code"
	}
	head := p.Subtle("╭─") + " " + p.Muted(label)
	if _, err := io.WriteString(r.w, head+"\n"); err != nil {
		return err
	}
	code := strings.TrimRight(body, "\n")
	highlighted := highlight(lang, code, p)
	lines := []string{}
	if highlighted != "" {
		lines = strings.Split(strings.TrimRight(highlighted, "\n"), "\n")
	} else {
		lines = strings.Split(code, "\n")
	}
	for _, line := range lines {
		if _, err := io.WriteString(r.w, p.Subtle("│ ")+line+"\n"); err != nil {
			return err
		}
	}
	_, err := io.WriteString(r.w, p.Subtle("╰─")+"\n")
	return err
}

func highlight(lang, code string, p Palette) string {
	if !p.Enabled || code == "" || !theme.CodeHighlight() {
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
	style := theme.ChromaStyle()
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
	cols := 0
	for _, cells := range parsed {
		if cells != nil {
			cols = len(cells)
			break
		}
	}
	if cols > 0 {
		for i, cells := range parsed {
			if cells == nil {
				continue
			}
			parsed[i] = fitRow(cells, cols)
		}
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
	first := true
	for _, cells := range parsed {
		if cells == nil {
			var b strings.Builder
			b.WriteString(p.Subtle("│"))
			for i, w := range widths {
				if i > 0 {
					b.WriteString(p.Subtle("┼"))
				}
				b.WriteString(p.Subtle(strings.Repeat("─", w+2)))
			}
			b.WriteString(p.Subtle("│") + "\n")
			if _, err := io.WriteString(r.w, b.String()); err != nil {
				return err
			}
			first = false
			continue
		}
		var b strings.Builder
		b.WriteString(p.Subtle("│"))
		for i, w := range widths {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			n := w - displayLen(cell)
			if n < 0 {
				n = 0
			}
			pad := cell + strings.Repeat(" ", n)
			if first {
				pad = p.Heading(pad)
			}
			b.WriteString(" " + pad + " " + p.Subtle("│"))
		}
		b.WriteByte('\n')
		if _, err := io.WriteString(r.w, b.String()); err != nil {
			return err
		}
		first = false
	}
	return nil
}

func splitRow(s string) []string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "|") {
		s = s[1:]
	}
	if strings.HasSuffix(s, "|") && !strings.HasSuffix(s, `\|`) {
		s = s[:len(s)-1]
	}
	var cells []string
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && s[i+1] == '|' {
			b.WriteByte('|')
			i++
			continue
		}
		if s[i] == '`' {
			if n := codeSpanLen(s[i:]); n > 0 {
				b.WriteString(unescapePipes(s[i : i+n]))
				i += n - 1
				continue
			}
		}
		if s[i] == '|' {
			cells = append(cells, strings.TrimSpace(b.String()))
			b.Reset()
			continue
		}
		b.WriteByte(s[i])
	}
	cells = append(cells, strings.TrimSpace(b.String()))
	return cells
}

func unescapePipes(s string) string {
	return strings.ReplaceAll(s, `\|`, "|")
}

func codeSpanLen(s string) int {
	if s == "" || s[0] != '`' {
		return 0
	}
	n := 0
	for n < len(s) && s[n] == '`' {
		n++
	}
	closer := s[:n]
	rest := s[n:]
	j := strings.Index(rest, closer)
	if j < 0 {
		return 0
	}
	return n + j + n
}

func fitRow(cells []string, cols int) []string {
	if cols < 1 {
		return cells
	}
	if len(cells) < cols {
		out := make([]string, cols)
		copy(out, cells)
		return out
	}
	if len(cells) == cols {
		return cells
	}
	merged := strings.Join(cells[cols-1:], " | ")
	out := make([]string, cols)
	copy(out, cells[:cols-1])
	out[cols-1] = merged
	return out
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
	return theme.DisplayWidth(s)
}

func inline(p Palette, s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '`' {
			if j := strings.IndexByte(s[i+1:], '`'); j >= 0 {
				b.WriteString(p.Accent(s[i+1 : i+1+j]))
				i += j + 2
				continue
			}
		}
		if strings.HasPrefix(s[i:], "[") {
			if text, url, n, ok := parseLink(s[i:]); ok {
				b.WriteString(p.Link(text))
				if url != "" && url != text {
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
