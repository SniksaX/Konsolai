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
	// Only the options line repeats after a stray key; the summary and preview stay put.
	for {
		fmt.Fprint(w, PromptOptions())
		b, err := p.nextKey()
		if err != nil {
			fmt.Fprintln(w)
			return 0, "", fmt.Errorf("ui: read prompt: %w", err)
		}
		d, ok := decisionFor(b)
		if !ok {
			fmt.Fprintln(w, "  "+Current().Warn("answer y, n, a or q"))
			continue
		}
		if d == ApproveAll {
			p.approveAll = true
		}
		fmt.Fprintln(w, "  "+decisionLabel(d))
		fmt.Fprintln(w)
		return d, "", nil
	}
}

func decisionFor(b byte) (Decision, bool) {
	switch unicode.ToLower(rune(b)) {
	case 'y':
		return Approve, true
	case 'a':
		return ApproveAll, true
	case 'n':
		return Reject, true
	case 'q', 0x03, 0x1b:
		return Quit, true
	}
	return 0, false
}

// decisionLabel echoes the answer on the options line so the scrollback shows what was chosen.
func decisionLabel(d Decision) string {
	p := Current()
	switch d {
	case Approve:
		return p.Success("✓ approved")
	case ApproveAll:
		return p.Success("✓ approved all")
	case Reject:
		return p.Warn("✗ rejected")
	}
	return p.Muted("quit")
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
