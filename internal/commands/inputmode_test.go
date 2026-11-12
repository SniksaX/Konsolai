package commands

import (
	"context"
	"testing"
)

type fakeMode struct {
	label string
	on    bool
	lines []string
	err   error
}

func (f *fakeMode) Label() string { return f.label }
func (f *fakeMode) Active() bool  { return f.on }
func (f *fakeMode) Handle(_ context.Context, _ *Env, line string) error {
	f.lines = append(f.lines, line)
	return f.err
}

func TestRegisterInputMode(t *testing.T) {
	saved := inputModes
	t.Cleanup(func() { inputModes = saved })
	inputModes = nil

	if name, mode := ActiveInputMode(); name != "" || mode != nil {
		t.Fatalf("an empty registry reported %q", name)
	}
	idle := &fakeMode{label: "idle"}
	RegisterInputMode("idle", idle)
	RegisterInputMode("", idle)
	RegisterInputMode("nil", nil)
	if name, mode := ActiveInputMode(); name != "" || mode != nil {
		t.Fatalf("an inactive mode was returned: %q", name)
	}

	live := &fakeMode{label: "live", on: true}
	RegisterInputMode("live", live)
	name, mode := ActiveInputMode()
	if name != "live" || mode != live {
		t.Fatalf("active mode %q %v", name, mode)
	}
	if mode.Label() != "live" {
		t.Fatalf("label %q", mode.Label())
	}
	if err := mode.Handle(context.Background(), nil, "a line"); err != nil {
		t.Fatal(err)
	}
	if len(live.lines) != 1 || live.lines[0] != "a line" {
		t.Fatalf("handled lines %v", live.lines)
	}
}
