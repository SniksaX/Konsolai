package shellmode

import (
	"strings"
	"unicode/utf8"

	"github.com/SniksaX/Konsolai/internal/redact"
)

const (
	maxKept      = 5
	contextCap   = 4 * 1024
	captureCap   = 64 * 1024
	truncateMark = "\n... [truncated] ...\n"
)

type Entry struct {
	Cmd    string
	Output string
}

type History struct {
	items []Entry
}

func (h *History) Add(cmd, output string) {
	if h == nil {
		return
	}
	cmd = strings.TrimRight(cmd, "\n")
	if strings.TrimSpace(cmd) == "" {
		return
	}
	h.items = append(h.items, Entry{
		Cmd:    redact.String(cmd),
		Output: redact.String(clipBytes(output, contextCap)),
	})
	if len(h.items) > maxKept {
		h.items = append([]Entry(nil), h.items[len(h.items)-maxKept:]...)
	}
}

func (h *History) Clear() {
	if h == nil {
		return
	}
	h.items = nil
}

func (h *History) Len() int {
	if h == nil {
		return 0
	}
	return len(h.items)
}

func (h *History) Items() []Entry {
	if h == nil {
		return nil
	}
	out := make([]Entry, len(h.items))
	copy(out, h.items)
	return out
}

func (h *History) Format() string {
	if h == nil || len(h.items) == 0 {
		return ""
	}
	var b strings.Builder
	for _, e := range h.items {
		b.WriteString("<shell_history>\n")
		b.WriteString(e.Cmd)
		if e.Output != "" {
			b.WriteString("\n")
			b.WriteString(e.Output)
		}
		b.WriteString("\n</shell_history>\n")
	}
	return b.String()
}

func clipBytes(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func CapHeadTail(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	keep := n / 2
	if keep < 1 {
		keep = 1
	}
	head, tail := s[:keep], s[len(s)-keep:]
	for len(head) > 0 && !utf8.ValidString(head) {
		head = head[:len(head)-1]
	}
	for len(tail) > 0 && !utf8.ValidString(tail) {
		tail = tail[1:]
	}
	return head + truncateMark + tail
}
