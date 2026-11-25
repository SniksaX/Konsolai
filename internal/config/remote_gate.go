package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/term"
)

var (
	remoteGate   sync.Mutex
	remoteAsked  bool
	remoteAgreed bool
	remoteTokens int
	remotePrompt = askRemote
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

// NoteRemoteUsage adds tokens spent on remote calls to the process total.
func NoteRemoteUsage(n int) {
	if n <= 0 {
		return
	}
	remoteGate.Lock()
	remoteTokens += n
	remoteGate.Unlock()
}

func askRemote(host string) (bool, error) {
	fmt.Fprintf(os.Stderr, "this sends your prompts and file contents to %s [y/N]: ", host)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
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
		return fmt.Errorf("config: remote use was declined for this process; model %q (%s) is refused", model, host)
	}
	remoteGate.Unlock()

	if assumeYes {
		remoteGate.Lock()
		remoteAsked, remoteAgreed = true, true
		remoteGate.Unlock()
		return nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("config: model %q is remote (%s); a non-interactive run refuses it without --yes", model, host)
	}

	remoteGate.Lock()
	ask := remotePrompt
	remoteGate.Unlock()
	okAsk, err := ask(host)
	if err != nil {
		return fmt.Errorf("config: remote confirmation: %w", err)
	}
	remoteGate.Lock()
	remoteAsked = true
	remoteAgreed = okAsk
	remoteGate.Unlock()
	if !okAsk {
		return fmt.Errorf("config: remote use declined; model %q (%s) is refused", model, host)
	}
	return nil
}
