package ui

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

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
	_, err := io.WriteString(g.w, p.Muted("│ ")+p.Muted(string(line))+"\n")
	return err
}

// Numbered prefixes each line with a muted line number.
func Numbered(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
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
	return "[" + p.Accent("y") + "]es [" + p.Accent("n") + "]o [" + p.Accent("a") + "]ll [" + p.Accent("q") + "]uit "
}

func IntentBadge(name string) string {
	p := Current()
	return p.Accent("◆") + " " + p.Muted(name)
}
