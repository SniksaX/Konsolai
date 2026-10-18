package ui

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/SniksaX/Konsolai/internal/ui/theme"
)

const (
	CallIndent = "  "
	OutIndent  = "    "
)

var reLeadNum = regexp.MustCompile(`^\d+\.\s`)

// Gutter prefixes each line with a muted bar for live tool output.
type Gutter struct {
	w       io.Writer
	pending []byte
}

func NewGutter(w io.Writer) *Gutter { return &Gutter{w: w} }

func (g *Gutter) Write(p []byte) (int, error) {
	n := len(p)
	g.pending = append(g.pending, p...)
	for {
		i := bytes.IndexByte(g.pending, '\n')
		if i < 0 {
			return n, nil
		}
		line := g.pending[:i]
		g.pending = g.pending[i+1:]
		if err := g.emit(line); err != nil {
			return n, err
		}
	}
}

func (g *Gutter) emit(line []byte) error {
	p := Current()
	_, err := io.WriteString(g.w, OutIndent+p.Subtle("│ ")+theme.Style(theme.Primary, string(line))+"\n")
	return err
}

// Numbered prefixes each line with a muted line number.
// Lines that already start with "N." keep that numbering (plan previews).
func Numbered(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if alreadyNumbered(lines) {
		var b strings.Builder
		for _, line := range lines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		return b.String()
	}
	width := len(strconv.Itoa(len(lines)))
	p := Current()
	var b strings.Builder
	for i, line := range lines {
		b.WriteString(p.Muted(fmt.Sprintf("%*d", width, i+1)))
		b.WriteByte(' ')
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func alreadyNumbered(lines []string) bool {
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		return reLeadNum.MatchString(t)
	}
	return false
}

// Indent prefixes every non-empty line with n spaces. Empty lines stay empty.
func Indent(s string, n int) string {
	if s == "" || n <= 0 {
		return s
	}
	pad := strings.Repeat(" ", n)
	ends := strings.HasSuffix(s, "\n")
	body := strings.TrimRight(s, "\n")
	lines := strings.Split(body, "\n")
	var b strings.Builder
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			b.WriteString(pad)
		}
		b.WriteString(line)
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	if ends {
		b.WriteByte('\n')
	}
	return b.String()
}

// IndentWriter prefixes each non-empty line with n spaces.
type IndentWriter struct {
	w    io.Writer
	pad  string
	atNL bool
}

func NewIndentWriter(w io.Writer, n int) *IndentWriter {
	return &IndentWriter{w: w, pad: strings.Repeat(" ", n), atNL: true}
}

func (i *IndentWriter) Write(p []byte) (int, error) {
	if i == nil || i.w == nil {
		return len(p), nil
	}
	n := 0
	for len(p) > 0 {
		if i.atNL {
			if p[0] == '\n' {
				nw, err := i.w.Write(p[:1])
				n += nw
				if err != nil {
					return n, err
				}
				p = p[1:]
				continue
			}
			if _, err := io.WriteString(i.w, i.pad); err != nil {
				return n, err
			}
			i.atNL = false
		}
		j := bytes.IndexByte(p, '\n')
		if j < 0 {
			nw, err := i.w.Write(p)
			n += nw
			return n, err
		}
		nw, err := i.w.Write(p[:j+1])
		n += nw
		if err != nil {
			return n, err
		}
		i.atNL = true
		p = p[j+1:]
	}
	return n, nil
}

func isDiffPreview(s string) bool {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "diff ") || strings.HasPrefix(t, "--- ") || strings.HasPrefix(t, "@@ ") {
		return true
	}
	return strings.Contains(s, "\n@@ ") || strings.Contains(s, "\n+++ ")
}

func ColorDiff(s string) string {
	p := Current()
	if s == "" {
		return s
	}
	var b bytes.Buffer
	lines := bytes.Split([]byte(s), []byte("\n"))
	for i, line := range lines {
		t := string(line)
		switch {
		case len(t) > 0 && t[0] == '+':
			t = p.Success(t)
		case len(t) > 0 && t[0] == '-':
			t = p.Error(t)
		case bytes.HasPrefix(line, []byte("@@")):
			t = p.Accent(t)
		}
		b.WriteString(t)
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func PromptOptions() string {
	p := Current()
	return "[" + p.Accent("y") + "]es [" + p.Accent("n") + "]o [" + p.Accent("a") + "]ll [" + p.Accent("q") + "]uit"
}

func IntentBadge(name string) string {
	return IntentBadgeLine(name, false)
}

func IntentBadgeLine(name string, escalated bool) string {
	p := Current()
	line := "  " + p.Accent("◆") + " " + p.Heading(name)
	if escalated {
		line += p.Muted(" · escalated")
	}
	return line
}
