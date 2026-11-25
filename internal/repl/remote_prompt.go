package repl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/ui"
)

// remotePromptText is the remote gate question. It mirrors the one-shot
// prompt in config so both paths read the same.
func remotePromptText(host string) string {
	return fmt.Sprintf("this sends your prompts and file contents to %s [y/N]: ", host)
}

// remoteReply classifies one prompt line: yes, no, or invalid (the prompt is
// asked again once, never treated as a decline).
func remoteReply(line string) (yes, valid bool) {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, true
	case "n", "no":
		return false, true
	}
	return false, false
}

// remoteAnswer is the historical helper: true only for an explicit yes.
func remoteAnswer(line string) bool {
	yes, valid := remoteReply(line)
	return yes && valid
}

// askRemoteOnEditor reads the remote confirmation through the REPL editor
// instead of os.Stdin, so history, arrows and paste work and the REPL keeps
// ownership of the terminal. Ctrl+C and Ctrl+D decline, like a typed n.
func askRemoteOnEditor(host string) (bool, error) {
	// Stop the spinner and clear its line before the modal prompt: a live
	// spinner must never paint over the y/N question.
	ui.PauseLive()
	defer ui.ResumeLive()
	for attempt := 0; attempt < 2; attempt++ {
		line, err := commands.ReadLineFromEditor(context.Background(), remotePromptText(host))
		if err != nil {
			if errors.Is(err, ErrInterrupt) || errors.Is(err, ErrEOF) || errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, err
		}
		if yes, valid := remoteReply(line); valid {
			return yes, nil
		}
	}
	return false, fmt.Errorf("config: remote confirmation needs y or n")
}

// wireRemotePrompt routes the remote y/n question through the REPL's editor
// reader. The returned func puts the stdin prompt back when the REPL exits.
func wireRemotePrompt() (restore func()) {
	config.SetRemotePrompt(askRemoteOnEditor)
	return func() { config.SetRemotePrompt(nil) }
}
