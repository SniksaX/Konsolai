package commands

import (
	"context"
	"testing"
)

func TestRegisterExtraAddsCommand(t *testing.T) {
	saved := extra
	t.Cleanup(func() { extra = saved })
	extra = nil
	cmd := Command{
		Name: "zz-probe", Usage: "zz-probe <x>", Summary: "probe",
		Args: []Arg{{Name: "x", Required: true}},
		Tree: func(*Env) []Choice {
			return []Choice{{Value: "one", Description: "first", Terminal: true}}
		},
		Run: func(context.Context, *Env, []string) int { return 0 },
	}
	RegisterExtra(cmd)
	got := Default().Lookup("zz-probe")
	if got == nil {
		t.Fatal("an extra command is missing from Default()")
	}
	if len(got.Args) != 1 || got.Tree == nil {
		t.Fatalf("the argument tree was lost: %+v", got)
	}
	kids := got.Tree(nil)
	if len(kids) != 1 || kids[0].Value != "one" {
		t.Fatalf("tree %+v", kids)
	}
	if Default().Lookup("help") == nil {
		t.Fatal("Default() lost its built-in commands")
	}
}
