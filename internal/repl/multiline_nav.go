package repl

// Multi-line navigation rules, in one place. The cursor is a flat rune index
// into a line that may contain '\n':
//
//   - Up/Down move between lines at the same column, clamped to the shorter
//     line, and recall history only when already on the first/last line.
//   - Left at column 0 moves to the end of the previous line; Right at the end
//     of a line moves to the start of the next.
//   - Backspace at column 0 joins with the previous line; Delete at the end of
//     a line joins the next one, and does nothing at the end of the buffer.
//   - Home/End, and Ctrl+A/Ctrl+E, act on the current line only.
//
// The readline widgets are overridden with these rules so every binding that
// reaches them (plain, CSI, SS3, and modifier forms) behaves the same.

import (
	"github.com/reeflective/readline/inputrc"
)

// navLine describes the logical line holding a cursor position.
type navLine struct {
	index int
	start int // first rune of the line
	end   int // index of the line's newline, or len for the last line
	count int // number of logical lines
}

func navLineAt(line []rune, pos int) navLine {
	if pos < 0 {
		pos = 0
	}
	if pos > len(line) {
		pos = len(line)
	}
	span := navLine{}
	for i := 0; i < pos; i++ {
		if line[i] == inputrc.Newline {
			span.index++
			span.start = i + 1
		}
	}
	span.end = len(line)
	for i := span.start; i < len(line); i++ {
		if line[i] == inputrc.Newline {
			span.end = i
			break
		}
	}
	span.count = 1
	for _, r := range line {
		if r == inputrc.Newline {
			span.count++
		}
	}
	return span
}

func (r *rlReader) navSet(pos int) {
	if r.sh == nil || r.sh.Cursor() == nil || r.sh.Line() == nil {
		return
	}
	r.sh.Cursor().Set(pos)
}

func (r *rlReader) navLine() ([]rune, int, navLine, bool) {
	if r.sh == nil || r.sh.Cursor() == nil || r.sh.Line() == nil {
		return nil, 0, navLine{}, false
	}
	line := []rune(*r.sh.Line())
	pos := r.sh.Cursor().Pos()
	return line, pos, navLineAt(line, pos), true
}

func (r *rlReader) navWidth(span navLine, pos int) int {
	return pos - span.start
}

// navUp moves one line up, or recalls the previous history entry on line one.
func (r *rlReader) navUp() {
	line, pos, span, ok := r.navLine()
	if !ok {
		return
	}
	r.sh.History.SkipSave()
	if span.index == 0 {
		r.callWidget("previous-history")
		return
	}
	width := r.navWidth(span, pos)
	target := span.start - 1 // end of the previous line
	begin := target
	for begin > 0 && line[begin-1] != inputrc.Newline {
		begin--
	}
	if p := begin + width; p < target {
		target = p
	}
	r.navSet(target)
}

// navDown moves one line down, or recalls the next history entry on the last line.
func (r *rlReader) navDown() {
	line, pos, span, ok := r.navLine()
	if !ok {
		return
	}
	r.sh.History.SkipSave()
	if span.index >= span.count-1 {
		r.callWidget("next-history")
		return
	}
	width := r.navWidth(span, pos)
	begin := span.end + 1
	end := len(line)
	for i := begin; i < len(line); i++ {
		if line[i] == inputrc.Newline {
			end = i
			break
		}
	}
	target := begin + width
	if target > end {
		target = end
	}
	r.navSet(target)
}

// navLeft moves one column left; column 0 is the end of the previous line.
func (r *rlReader) navLeft() {
	_, pos, span, ok := r.navLine()
	if !ok || pos == 0 {
		return
	}
	r.sh.History.SkipSave()
	target := pos - 1
	if pos == span.start && span.index > 0 {
		target = span.start - 1 // the newline is the previous line's end
	}
	r.navSet(target)
}

// navRight moves one column right; a line end is the start of the next line.
func (r *rlReader) navRight() {
	line, pos, span, ok := r.navLine()
	if !ok {
		return
	}
	r.sh.History.SkipSave()
	if pos < span.end {
		r.navSet(pos + 1)
		return
	}
	if span.index < span.count-1 && pos < len(line) {
		r.navSet(pos + 1)
	}
}

// navHome is Home/Ctrl+A: the start of the current line.
func (r *rlReader) navHome() {
	_, _, span, ok := r.navLine()
	if !ok {
		return
	}
	r.sh.History.SkipSave()
	r.navSet(span.start)
}

// navEnd is End/Ctrl+E: the end of the current line, in append position.
func (r *rlReader) navEnd() {
	_, _, span, ok := r.navLine()
	if !ok {
		return
	}
	r.sh.History.SkipSave()
	r.navSet(span.end)
}

// navBackspace joins with the previous line at column 0.
func (r *rlReader) navBackspace() {
	_, pos, span, ok := r.navLine()
	if !ok || pos == 0 {
		return
	}
	r.sh.History.SkipSave()
	r.sh.Line().CutRune(pos - 1)
	target := pos - 1
	if target < span.start && target < 0 {
		target = 0
	}
	r.navSet(target)
}

// navDelete joins the next line when the cursor is at the line end.
func (r *rlReader) navDelete() {
	line, pos, span, ok := r.navLine()
	if !ok || pos >= len(line) {
		return
	}
	r.sh.History.SkipSave()
	if pos >= span.end {
		if span.index >= span.count-1 {
			return // end of the buffer: nothing to join
		}
		r.sh.Line().CutRune(pos) // the newline; cursor stays at the join point
		return
	}
	r.sh.Line().CutRune(pos)
}
