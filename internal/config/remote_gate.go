package config

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"golang.org/x/term"
)

var (
	remoteGate       sync.Mutex
	remoteAsked      bool
	remoteAgreed     bool
	remoteTokens     int
	remotePrompt     = askRemote
	remoteGateBefore func()
	remoteGateAfter  func()
	// remoteInteractive is the terminal check; tests and embedders override it.
	remoteInteractive = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
)

// ResetRemoteState forgets the process-wide remote decision and token total.
func ResetRemoteState() {
	remoteGate.Lock()
	remoteAsked = false
	remoteAgreed = false
	remoteTokens = 0
	remoteGate.Unlock()
}

// SetRemotePrompt replaces the y/n prompt (tests).
func SetRemotePrompt(fn func(host string) (bool, error)) {
	remoteGate.Lock()
	if fn == nil {
		remotePrompt = askRemote
	} else {
		remotePrompt = fn
	}
	remoteGate.Unlock()
}

// SetRemoteInteractive overrides the terminal check used by the confirmation.
func SetRemoteInteractive(fn func() bool) {
	remoteGate.Lock()
	if fn == nil {
		remoteInteractive = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	} else {
		remoteInteractive = fn
	}
	remoteGate.Unlock()
}

// NoteRemoteUsage adds tokens spent on remote calls to the process total.
func NoteRemoteUsage(n int) {
	if n <= 0 {
		return
	}
	remoteGate.Lock()
	remoteTokens += n
	remoteGate.Unlock()
}

// SetRemoteGateHooks installs the before/after paint guards around the
// confirmation prompt (the REPL pauses its spinner there). Nil clears them.
func SetRemoteGateHooks(before, after func()) {
	remoteGate.Lock()
	remoteGateBefore, remoteGateAfter = before, after
	remoteGate.Unlock()
}

func askRemote(host string) (bool, error) {
	reader := bufio.NewReader(os.Stdin)
	// An empty line or a stray key is not a decision: ask again once, then
	// fail instead of silently declining.
	for attempt := 0; attempt < 2; attempt++ {
		fmt.Fprintf(os.Stderr, "this sends your prompts and file contents to %s [y/N]: ", host)
		line, err := reader.ReadString('\n')
		if err != nil && strings.TrimSpace(line) == "" {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
	}
	return false, fmt.Errorf("config: remote confirmation needs y or n")
}

// RemoteCallPolicy is the per-call gate for one remote model.
func (c Config) RemoteCallPolicy(role, model string, assumeYes bool) func() error {
	return func() error { return c.remoteCallError(role, model, assumeYes) }
}

func (c Config) remoteCallError(role, model string, assumeYes bool) error {
	m, ok := c.Models[model]
	if !ok || !m.Remote() {
		return nil
	}
	host := URLHost(m.Endpoint)
	if !c.Privacy.AllowRemote {
		return fmt.Errorf("config: privacy.allow_remote is false; model %q is remote (%s) and cannot be used", model, host)
	}
	if !c.RemoteRoleAllowed(role) {
		return fmt.Errorf("config: privacy.remote_roles does not allow role %q to use remote model %q (%s)", role, model, host)
	}

	remoteGate.Lock()
	if cap := c.Privacy.SessionTokenCap; cap > 0 && remoteTokens > cap {
		used := remoteTokens
		remoteGate.Unlock()
		return fmt.Errorf("config: privacy.session_token_cap reached (%d > %d remote tokens); remote calls are stopped", used, cap)
	}
	if remoteAgreed {
		remoteGate.Unlock()
		return nil
	}
	if remoteAsked {
		remoteGate.Unlock()
		return fmt.Errorf("config: remote use declined for this process; model %q (%s) is refused; run /remote allow to reset", model, host)
	}
	remoteGate.Unlock()

	if assumeYes {
		remoteGate.Lock()
		remoteAsked, remoteAgreed = true, true
		remoteGate.Unlock()
		return nil
	}
	remoteGate.Lock()
	interactive := remoteInteractive
	remoteGate.Unlock()
	if !interactive() {
		return fmt.Errorf("config: model %q is remote (%s); a non-interactive run refuses it without --yes; run /remote allow in the REPL to reset", model, host)
	}

	remoteGate.Lock()
	ask := remotePrompt
	before, after := remoteGateBefore, remoteGateAfter
	remoteGate.Unlock()
	if before != nil {
		before()
	}
	okAsk, err := ask(host)
	if after != nil {
		after()
	}
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
			remoteGate.Lock()
			remoteAsked = true
			remoteAgreed = false
			remoteGate.Unlock()
			return fmt.Errorf("config: remote confirmation got no answer; remote use declined for this process; restart konsolai or run /remote allow to reset")
		}
		return fmt.Errorf("config: remote confirmation: %w", err)
	}
	remoteGate.Lock()
	remoteAsked = true
	remoteAgreed = okAsk
	remoteGate.Unlock()
	if !okAsk {
		return fmt.Errorf("config: remote use declined; model %q (%s) is refused for this process; restart konsolai or run /remote allow to reset", model, host)
	}
	return nil
}
