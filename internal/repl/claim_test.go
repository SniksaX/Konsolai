package repl

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/turn"
)

// claimMode takes every /stolen line and ignores the rest.
type claimMode struct {
	on      bool
	claimed []string
}

func (c *claimMode) Label() string                                       { return "claim" }
func (c *claimMode) Active() bool                                        { return c.on }
func (c *claimMode) Handle(context.Context, *commands.Env, string) error { return nil }
func (c *claimMode) Claim(line string) (bool, error) {
	c.claimed = append(c.claimed, line)
	return strings.HasPrefix(strings.TrimSpace(line), "/stolen"), nil
}

func TestActiveModeClaimsASlashLine(t *testing.T) {
	m := &claimMode{on: true}
	commands.RegisterInputMode("claim-test", m)
	t.Cleanup(func() { m.on = false })

	var out, errb bytes.Buffer
	env := &commands.Env{
		Profile: config.WithMain("http://127.0.0.1:9/v1", "m"),
		Out:     &out,
		Err:     &errb,
		Cwd:     t.TempDir(),
		Version: "test",
	}
	code := Run(context.Background(), Config{
		Runner:  &turn.Runner{Profile: env.Profile, Out: &out, Err: &errb},
		Env:     env,
		Reg:     commands.Default(),
		Reader:  &scriptReader{lines: []string{"/stolen", "/exit"}},
		Version: "test",
		Model:   "m",
		Out:     &out,
		Err:     &errb,
	})
	if code != 0 {
		t.Fatalf("exit %d %s", code, errb.String())
	}
	if len(m.claimed) == 0 || m.claimed[0] != "/stolen" {
		t.Fatalf("claimed %v", m.claimed)
	}
	if strings.Contains(errb.String(), "unknown command") {
		t.Fatalf("the claimed line was dispatched anyway: %q", errb.String())
	}

	// An inactive mode is not asked, so the line dispatches as before.
	m.on = false
	m.claimed = nil
	out.Reset()
	errb.Reset()
	if code := Run(context.Background(), Config{
		Runner:  &turn.Runner{Profile: env.Profile, Out: &out, Err: &errb},
		Env:     env,
		Reg:     commands.Default(),
		Reader:  &scriptReader{lines: []string{"/stolen", "/exit"}},
		Version: "test",
		Model:   "m",
		Out:     &out,
		Err:     &errb,
	}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(m.claimed) != 0 {
		t.Fatalf("an inactive mode claimed %v", m.claimed)
	}
	if !strings.Contains(errb.String(), "unknown command") {
		t.Fatalf("the line did not dispatch: %q", errb.String())
	}
}
