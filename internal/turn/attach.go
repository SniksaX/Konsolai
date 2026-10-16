package turn

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/siraj/konsolai/internal/redact"
	"github.com/siraj/konsolai/internal/repo"
	"github.com/siraj/konsolai/internal/safety"
	"github.com/siraj/konsolai/internal/tools"
	"github.com/siraj/konsolai/internal/ui"
)

const attachCap = 32 * 1024

type Attachment struct {
	Path    string
	Display string
	Size    int
	Body    string
}

func ExpandMentions(ctx context.Context, cwd, text string) (string, []Attachment) {
	if strings.TrimSpace(text) == "" {
		return text, nil
	}
	g, err := safety.NewAt(repo.Root(cwd), cwd)
	if err != nil {
		return text, nil
	}
	var atts []Attachment
	var b strings.Builder
	i := 0
	for i < len(text) {
		if text[i] == '\\' && i+1 < len(text) && text[i+1] == '@' {
			b.WriteByte('@')
			i += 2
			continue
		}
		if text[i] == '@' && (i == 0 || isWordBoundary(rune(text[i-1]))) {
			j := i + 1
			for j < len(text) && !isTokenEnd(rune(text[j])) {
				j++
			}
			tok := text[i+1 : j]
			if tok != "" {
				if att, ok := loadAttach(ctx, g, cwd, tok); ok {
					atts = append(atts, att)
					i = j
					continue
				}
			}
		}
		b.WriteByte(text[i])
		i++
	}
	if len(atts) == 0 {
		return text, nil
	}
	out := strings.TrimSpace(b.String())
	var blocks strings.Builder
	blocks.WriteString(out)
	for _, a := range atts {
		blocks.WriteString("\n\n<attached path=\"")
		blocks.WriteString(a.Display)
		blocks.WriteString("\">\n")
		blocks.WriteString(a.Body)
		blocks.WriteString("\n</attached>")
	}
	return blocks.String(), atts
}

func PrintAttachments(w io.Writer, atts []Attachment) {
	p := ui.Current()
	for _, a := range atts {
		fmt.Fprintln(w, p.Muted(fmt.Sprintf("attached %s (%s)", a.Display, formatAttachSize(a.Size))))
	}
}

func formatAttachSize(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f KiB", float64(n)/1024)
}

func isWordBoundary(r rune) bool {
	return unicode.IsSpace(r) || r == '(' || r == '[' || r == '{' || r == '"' || r == '\''
}

func isTokenEnd(r rune) bool {
	return unicode.IsSpace(r) || r == ',' || r == ';' || r == ')' || r == ']' || r == '}'
}

func loadAttach(ctx context.Context, g *safety.Guard, cwd, tok string) (Attachment, bool) {
	abs, err := g.ResolvePath(tok)
	if err != nil {
		return Attachment{}, false
	}
	if _, err := os.Stat(abs); err != nil {
		return Attachment{}, false
	}
	rel := tok
	if r, err := filepath.Rel(cwd, abs); err == nil {
		rel = filepath.ToSlash(r)
	}
	body, n := attachBody(ctx, g, cwd, abs)
	return Attachment{Path: abs, Display: rel, Size: n, Body: body}, true
}

func attachBody(ctx context.Context, g *safety.Guard, cwd, abs string) (string, int) {
	fi, err := os.Stat(abs)
	if err != nil {
		return "", 0
	}
	if fi.IsDir() {
		rel, err := filepath.Rel(cwd, abs)
		if err != nil {
			rel = abs
		}
		lister := func(context.Context) ([]string, error) { return repo.Files(ctx, repo.Root(cwd)) }
		reg := tools.NewRegistry(g, io.Discard, lister, nil, nil)
		t, ok := reg.Get("outline")
		if !ok {
			return "", 0
		}
		act, err := t.Prepare(`{"path":` + quoteJSON(filepath.ToSlash(rel)) + `}`)
		if err != nil {
			return err.Error(), 0
		}
		res, err := act.Run(ctx)
		if err != nil {
			return err.Error(), 0
		}
		return res.Output, len(res.Output)
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return err.Error(), 0
	}
	n := len(b)
	text := string(b)
	if n > attachCap {
		text = string(b[:attachCap]) + "\n[truncated]"
	}
	if redact.SensitivePath(abs) {
		text, _ = redact.MaskValues(text)
	}
	return text, n
}

func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
