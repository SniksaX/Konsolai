package repl

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	chipMinLines = 11
	chipMinChars = 1000
)

var chipRe = regexp.MustCompile(`\[pasted #(\d+) · \d+ lines\]`)

type PasteStore struct {
	items []string
	last  int
}

func NewPasteStore() *PasteStore { return &PasteStore{} }

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n") + 1
	if strings.HasSuffix(s, "\n") {
		n--
	}
	if n < 1 {
		return 1
	}
	return n
}

func ShouldChip(text string) bool {
	return len(text) >= chipMinChars || countLines(text) >= chipMinLines
}

func chipToken(n, lines int) string {
	return fmt.Sprintf("[pasted #%d · %d lines]", n, lines)
}

func (p *PasteStore) MaybeChip(text string) (string, bool) {
	if p == nil || !ShouldChip(text) {
		return text, false
	}
	p.items = append(p.items, text)
	p.last = len(p.items)
	return chipToken(p.last, countLines(text)), true
}

func (p *PasteStore) Expand(line string) string {
	if p == nil || line == "" || !strings.Contains(line, "[pasted #") {
		return line
	}
	return chipRe.ReplaceAllStringFunc(line, func(m string) string {
		sub := chipRe.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m
		}
		n, err := strconv.Atoi(sub[1])
		if err != nil || n < 1 || n > len(p.items) {
			return m
		}
		return p.items[n-1]
	})
}

func (p *PasteStore) LastChip() (chip, raw string, ok bool) {
	if p == nil || p.last < 1 || p.last > len(p.items) {
		return "", "", false
	}
	raw = p.items[p.last-1]
	return chipToken(p.last, countLines(raw)), raw, true
}

// InputEditor is the testable line-edit abstraction used for paste chips
// and Ctrl+Z. The readline backend mirrors this behaviour.
type InputEditor struct {
	Store    *PasteStore
	line     string
	history  []string
	lastChip bool
}

func NewInputEditor(store *PasteStore) *InputEditor {
	if store == nil {
		store = NewPasteStore()
	}
	return &InputEditor{Store: store}
}

func (e *InputEditor) Line() string { return e.line }

func (e *InputEditor) Submit() string { return e.Store.Expand(e.line) }

func (e *InputEditor) Insert(text string) {
	e.history = append(e.history, e.line)
	chip, ok := e.Store.MaybeChip(text)
	if ok {
		e.line += chip
		e.lastChip = true
		return
	}
	e.line += text
	e.lastChip = false
}

func (e *InputEditor) Undo() {
	if e.lastChip {
		chip, raw, ok := e.Store.LastChip()
		if ok && strings.Contains(e.line, chip) {
			e.line = strings.Replace(e.line, chip, raw, 1)
			e.lastChip = false
			return
		}
	}
	if len(e.history) == 0 {
		return
	}
	e.line = e.history[len(e.history)-1]
	e.history = e.history[:len(e.history)-1]
	e.lastChip = false
}
