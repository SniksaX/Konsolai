package tools

import (
	"bytes"
	"fmt"
	"io"

	"github.com/siraj/konsolai/internal/ui"
)

const lineLimit = 60

type LineLimiter struct {
	out     io.Writer
	shown   int
	hidden  int
	pending []byte
}

func NewLineLimiter(out io.Writer) *LineLimiter {
	return &LineLimiter{out: out}
}

func (l *LineLimiter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			l.pending = append(l.pending, p...)
			return n, nil
		}
		line := append(l.pending, p[:i+1]...)
		l.pending = nil
		p = p[i+1:]
		if err := l.emit(line); err != nil {
			return n, err
		}
	}
	return n, nil
}

func (l *LineLimiter) emit(line []byte) error {
	if l.shown < lineLimit {
		l.shown++
		_, err := l.out.Write(line)
		return err
	}
	l.hidden++
	return nil
}

func (l *LineLimiter) Close(w io.Writer) {
	if len(l.pending) > 0 {
		_ = l.emit(l.pending)
		l.pending = nil
	}
	if l.hidden > 0 {
		fmt.Fprintln(w, ui.Dim(fmt.Sprintf("    … %d more lines hidden (/output to show all)", l.hidden)))
	}
}
