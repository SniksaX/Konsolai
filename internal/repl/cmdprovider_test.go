package repl

import (
	"context"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/commands"
)

// plainMode is a mode with no optional interfaces at all.
type plainMode struct{}

func (plainMode) Label() string { return "plain" }
func (plainMode) Active() bool  { return false }
func (plainMode) Handle(context.Context, *commands.Env, string) error {
	return nil
}

// providerMode is a mode that offers a few slash rows and records its claims.
type providerMode struct {
	on      bool
	rows    []commands.Choice
	claimed []string
	handled bool
}

func (p *providerMode) Label() string { return "mode" }
func (p *providerMode) Active() bool  { return p.on }
func (p *providerMode) Handle(context.Context, *commands.Env, string) error {
	return nil
}
func (p *providerMode) Claim(line string) (bool, error) {
	p.claimed = append(p.claimed, line)
	return p.handled, nil
}
func (p *providerMode) Commands() []commands.Choice { return p.rows }

// registerProvider installs a mode for one test and leaves it inactive after.
func registerProvider(t *testing.T, rows []commands.Choice) *providerMode {
	t.Helper()
	p := &providerMode{on: true, rows: rows, handled: true}
	commands.RegisterInputMode("testprovider", p)
	t.Cleanup(func() { p.on = false })
	return p
}

func TestCommandsInterfaceIsOptional(t *testing.T) {
	// A mode without Commands is still a mode; the menu simply has no source.
	var m commands.InputMode = plainMode{}
	if _, ok := m.(commands.CommandProvider); ok {
		t.Fatal("a plain mode must not satisfy the provider")
	}
	// An empty list means "not mine".
	registerProvider(t, nil)
	menu := newSlashMenu(commands.Default())
	menu.Sync("/history")
	if r := menu.Enter(); r.Action == actionSubmit {
		t.Fatal("an empty list must not claim the line")
	}
}

func TestProviderRowsOwnTheTypedLine(t *testing.T) {
	rows := []commands.Choice{
		{Value: "/history", Description: "list the sessions"},
		{Value: "/resume", Description: "open one session"},
	}
	p := registerProvider(t, rows)
	menu := newSlashMenu(commands.Default())
	menu.Sync("/history")
	res := menu.Enter()
	if res.Action != actionSubmit {
		t.Fatalf("Enter on a listed line: action %v, want submit", res.Action)
	}
	// Submit means the REPL hands the typed line to the mode first.
	if handled, err := claimLine(p, menu.Typed()); !handled || err != nil {
		t.Fatalf("claimLine: handled=%v err=%v", handled, err)
	}
	if len(p.claimed) != 1 || strings.TrimSpace(p.claimed[0]) != "/history" {
		t.Fatalf("claim did not see the line: %v", p.claimed)
	}
}

func TestUnlistedLineStillReachesTheRegistry(t *testing.T) {
	registerProvider(t, []commands.Choice{{Value: "/history", Description: "list"}})
	menu := newSlashMenu(commands.Default())
	menu.Sync("/help")
	if providerOwns("/help") {
		t.Fatal("a line the mode does not list must not be owned by it")
	}
	if !providerOwns("/history") {
		t.Fatal("a listed line must be owned")
	}
}
