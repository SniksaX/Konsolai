package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
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
	return p, nil
}

func (p *Prompter) ConfirmForced(summary, preview string) (Decision, string, error) {
	return p.confirm(summary, preview, true)
}

func (p *Prompter) Confirm(summary, preview string) (Decision, string, error) {
	return p.confirm(summary, preview, false)
}

func (p *Prompter) confirm(summary, preview string, force bool) (Decision, string, error) {
	if p.autoApprove || (!force && p.approveAll) {
		return Approve, "", nil
	}
	in := bufio.NewReader(p.tty)
	for {
		fmt.Fprintln(p.tty, summary)
		if preview != "" {
			fmt.Fprintln(p.tty, preview)
		}
		fmt.Fprint(p.tty, "[y]es [n]o [a]ll [q]uit ")
		line, err := in.ReadString('\n')
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
			fmt.Fprint(p.tty, "reason (optional): ")
			reason, err := in.ReadString('\n')
			if err != nil {
				return 0, "", fmt.Errorf("ui: read reason: %w", err)
			}
			return Reject, strings.TrimSpace(reason), nil
		}
	}
}

func (p *Prompter) Close() error {
	if p == nil || p.tty == nil {
		return nil
	}
	return p.tty.Close()
}
