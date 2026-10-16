package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type Decision int

const (
	Approve Decision = iota
	ApproveAll
	Reject
	Quit
)

type Prompter struct {
	tty         *os.File
	in          *bufio.Reader
	out         io.Writer
	autoApprove bool
	approveAll  bool
}

func OpenPrompter(autoApprove bool) (*Prompter, error) {
	p := &Prompter{autoApprove: autoApprove}
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		if autoApprove {
			return p, nil
		}
		return nil, fmt.Errorf("ui: open tty: %w", err)
	}
	p.tty = f
	p.in = bufio.NewReader(f)
	p.out = f
	return p, nil
}

func (p *Prompter) ConfirmForced(summary, preview string) (Decision, string, error) {
	return p.confirm(summary, preview, true)
}

func (p *Prompter) Confirm(summary, preview string) (Decision, string, error) {
	return p.confirm(summary, preview, false)
}

func (p *Prompter) writer() io.Writer {
	if p.out != nil {
		return p.out
	}
	return p.tty
}

func (p *Prompter) drain() {
	if p.tty == nil {
		return
	}
	_ = p.tty.SetReadDeadline(time.Now())
	var buf [256]byte
	for {
		_, err := p.tty.Read(buf[:])
		if err != nil {
			break
		}
	}
	_ = p.tty.SetReadDeadline(time.Time{})
	if p.in == nil {
		p.in = bufio.NewReader(p.tty)
	} else {
		p.in.Reset(p.tty)
	}
}

func (p *Prompter) confirm(summary, preview string, force bool) (Decision, string, error) {
	if p.autoApprove || (!force && p.approveAll) {
		return Approve, "", nil
	}
	if p.in == nil && p.tty != nil {
		p.in = bufio.NewReader(p.tty)
	}
	w := p.writer()
	p.drain()
	for {
		fmt.Fprintln(w, Current().Strong(summary))
		if preview != "" {
			if isDiffPreview(preview) {
				fmt.Fprintln(w, ColorDiff(preview))
			} else {
				fmt.Fprint(w, Numbered(preview))
			}
		}
		fmt.Fprint(w, PromptOptions())
		line, err := p.in.ReadString('\n')
		if err != nil {
			return 0, "", fmt.Errorf("ui: read prompt: %w", err)
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return Approve, "", nil
		case "a", "all":
			p.approveAll = true
			return ApproveAll, "", nil
		case "q", "quit":
			return Quit, "", nil
		case "n", "no":
			fmt.Fprint(w, "reason (optional): ")
			reason, err := p.in.ReadString('\n')
			if err != nil {
				return 0, "", fmt.Errorf("ui: read reason: %w", err)
			}
			return Reject, strings.TrimSpace(reason), nil
		default:
			fmt.Fprintln(w, Current().Warn("answer y, n, a or q"))
		}
	}
}

func (p *Prompter) Cancel() {
	if p == nil || p.tty == nil {
		return
	}
	_ = p.tty.SetReadDeadline(time.Now())
}

func (p *Prompter) Close() error {
	if p == nil || p.tty == nil {
		return nil
	}
	return p.tty.Close()
}
