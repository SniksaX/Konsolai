package theme

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/rivo/uniseg"
)

var unitWidth = map[rune]bool{
	'◆': true, '◇': true, '●': true, '○': true,
	'▸': true, '✓': true, '✗': true, '•': true, '›': true,
}

func DisplayWidth(s string) int {
	return displayPlain(StripANSI(s))
}

func displayPlain(s string) int {
	n := 0
	gr := uniseg.NewGraphemes(s)
	for gr.Next() {
		r := []rune(gr.Str())
		if len(r) == 1 && unitWidth[r[0]] {
			n++
			continue
		}
		n += uniseg.StringWidth(gr.Str())
	}
	return n
}

func VisibleWidth(s string) int {
	return DisplayWidth(s)
}

func StripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) {
			if s[i+1] == '[' {
				i += 2
				for i < len(s) && !((s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z')) {
					i++
				}
				if i < len(s) {
					i++
				}
				continue
			}
			if s[i+1] == ']' {
				i += 2
				for i < len(s) {
					if s[i] == 0x07 {
						i++
						break
					}
					if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
						i += 2
						break
					}
					i++
				}
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func TruncateChrome(s string, width int) string {
	if VisibleWidth(s) <= width {
		return s
	}
	return Truncate(StripANSI(s), width)
}

func VisualLines(s string, width int) int {
	if width < 1 {
		width = 80
	}
	n := 0
	for _, para := range strings.Split(s, "\n") {
		w := VisibleWidth(para)
		if w == 0 {
			n++
			continue
		}
		rows := (w + width - 1) / width
		if rows < 1 {
			rows = 1
		}
		n += rows
	}
	if n < 1 {
		return 1
	}
	return n
}

func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if DisplayWidth(s) <= width {
		return s
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	keep := width - 3
	var b strings.Builder
	n := 0
	gr := uniseg.NewGraphemes(s)
	for gr.Next() {
		w := DisplayWidth(gr.Str())
		if n+w > keep {
			break
		}
		b.WriteString(gr.Str())
		n += w
	}
	b.WriteString("...")
	return b.String()
}

func MiddleTruncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if DisplayWidth(s) <= width {
		return s
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	keep := width - 3
	left := keep / 2
	right := keep - left
	var head, tail strings.Builder
	n := 0
	gr := uniseg.NewGraphemes(s)
	for gr.Next() && n < left {
		w := DisplayWidth(gr.Str())
		if n+w > left {
			break
		}
		head.WriteString(gr.Str())
		n += w
	}
	rs := []rune(s)
	n = 0
	end := len(rs)
	for end > 0 && n < right {
		r := rs[end-1]
		w := DisplayWidth(string(r))
		if n+w > right {
			break
		}
		tail.WriteRune(r)
		n += w
		end--
	}
	rev := []rune(tail.String())
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return head.String() + "..." + string(rev)
}

func TermWidth() int {
	if c := os.Getenv("COLUMNS"); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n > 0 {
			return n
		}
	}
	type winsize struct {
		Row, Col, Xpixel, Ypixel uint16
	}
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	if errno == 0 && ws.Col > 0 {
		return int(ws.Col)
	}
	return 80
}

func WrapWidth() int {
	w := TermWidth() - 4
	if w > 100 {
		w = 100
	}
	if w < 20 {
		w = 20
	}
	return w
}

func Wrap(s string, width int) string {
	if width < 8 {
		width = 8
	}
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		if strings.TrimSpace(para) == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, wrapWords(para, width)...)
	}
	return strings.Join(lines, "\n")
}

func wrapWords(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{s}
	}
	var out []string
	var cur strings.Builder
	curW := 0
	for _, w := range words {
		ww := DisplayWidth(w)
		if curW == 0 {
			cur.WriteString(w)
			curW = ww
			continue
		}
		if curW+1+ww <= width {
			cur.WriteByte(' ')
			cur.WriteString(w)
			curW += 1 + ww
			continue
		}
		out = append(out, cur.String())
		cur.Reset()
		cur.WriteString(w)
		curW = ww
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
