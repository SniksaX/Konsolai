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

// remoteAnswer accepts the same words as the one-shot prompt.
func remoteAnswer(line string) bool {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// askRemoteOnEditor reads the remote confirmation through the REPL editor
// instead of os.Stdin, so history, arrows and paste work and the REPL keeps
// ownership of the terminal. Ctrl+C and Ctrl+D decline, like a typed n.
func askRemoteOnEditor(host string) (bool, error) {
	ui.HoldPrompt(true)
	defer ui.HoldPrompt(false)
	line, err := commands.ReadLineFromEditor(context.Background(), remotePromptText(host))
	if err != nil {
		if errors.Is(err, ErrInterrupt) || errors.Is(err, ErrEOF) || errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	return remoteAnswer(line), nil
}

// wireRemotePrompt routes the remote y/n question through the REPL's editor
// reader. The returned func puts the stdin prompt back when the REPL exits.
func wireRemotePrompt() (restore func()) {
	config.SetRemotePrompt(askRemoteOnEditor)
	return func() { config.SetRemotePrompt(nil) }
}
