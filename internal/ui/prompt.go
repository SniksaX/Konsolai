package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"
)

type Decision int

const (
	Approve Decision = iota
	ApproveAll
	Reject
	Quit
)

type Prompter struct {
	read        KeyReader
	out         io.Writer
	autoApprove bool
	approveAll  bool
}

func OpenPrompter(autoApprove bool) (*Prompter, error) {
	return &Prompter{autoApprove: autoApprove, out: os.Stdout}, nil
}

func (p *Prompter) ConfirmForced(summary, preview string) (Decision, string, error) {
	return p.confirm(summary, preview, true)
}

func (p *Prompter) Confirm(summary, preview string) (Decision, string, error) {
	return p.confirm(summary, preview, false)
}

func (p *Prompter) writer() io.Writer {
	if p != nil && p.out != nil {
		return p.out
	}
	return os.Stdout
}

func (p *Prompter) readKey() (byte, error) {
	if p != nil && p.read != nil {
		return p.read()
	}
	if fn := currentKeyReader(); fn != nil {
		return fn()
	}
	return readStdinKey()
}

func (p *Prompter) nextKey() (byte, error) {
	for {
		b, err := p.readKey()
		if err != nil {
			return 0, err
		}
		if b == 0 || b == '\r' || b == '\n' {
			continue
		}
		return b, nil
	}
}

func (p *Prompter) confirm(summary, preview string, force bool) (Decision, string, error) {
	if p.autoApprove || (!force && p.approveAll) {
		return Approve, "", nil
	}
	w := p.writer()
	BeforeOutput()
	for {
		fmt.Fprintln(w)
		fmt.Fprintln(w, Current().Heading(summary))
		if preview != "" {
			body := preview
			if isDiffPreview(preview) {
				body = ColorDiff(preview)
				if !strings.HasSuffix(body, "\n") {
					body += "\n"
				}
			} else {
				body = Numbered(preview)
			}
			fmt.Fprint(w, Indent(body, 4))
		}
		fmt.Fprintln(w, PromptOptions())
		b, err := p.nextKey()
		if err != nil {
			return 0, "", fmt.Errorf("ui: read prompt: %w", err)
		}
		switch unicode.ToLower(rune(b)) {
		case 'y':
			fmt.Fprintln(w)
			return Approve, "", nil
		case 'a':
			p.approveAll = true
			fmt.Fprintln(w)
			return ApproveAll, "", nil
		case 'q', 0x03, 0x1b:
			fmt.Fprintln(w)
			return Quit, "", nil
		case 'n':
			fmt.Fprintln(w)
			return Reject, "", nil
		default:
			fmt.Fprintln(w, Current().Warn("answer y, n, a or q"))
		}
	}
}

func (p *Prompter) Cancel() {
	_ = os.Stdin.SetReadDeadline(time.Now())
}

func (p *Prompter) Close() error {
	if p == nil {
		return nil
	}
	_ = os.Stdin.SetReadDeadline(time.Time{})
	return nil
}
