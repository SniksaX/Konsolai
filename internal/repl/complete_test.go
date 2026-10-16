package repl

import (
	"strings"
	"testing"

	"github.com/reeflective/readline"
	"github.com/siraj/konsolai/internal/commands"
)

func TestSlashItemsFilterAndArgs(t *testing.T) {
	reg := commands.Default()
	items := slashItems(reg, "/")
	if len(items) == 0 {
		t.Fatal("empty / list")
	}
	if items[0].Value != "/help" || items[0].Desc == "" {
		t.Fatalf("first %+v", items[0])
	}
	hel := slashItems(reg, "/hel")
	if len(hel) != 1 || hel[0].Value != "/help" {
		t.Fatalf("/hel %+v", hel)
	}
	copyArgs := slashItems(reg, "/copy ")
	got := map[string]string{}
	for _, it := range copyArgs {
		got[it.Value] = it.Desc
	}
	for _, n := range []string{"answer", "all", "code", "cmd"} {
		if got[n] == "" {
			t.Fatalf("copy missing %s in %+v", n, copyArgs)
		}
	}
	think := slashItems(reg, "/think o")
	if len(think) != 2 {
		t.Fatalf("think o %+v", think)
	}
	yes := slashItems(reg, "/yes ")
	if len(yes) != 2 {
		t.Fatalf("yes %+v", yes)
	}
	trace := slashItems(reg, "/trace of")
	if len(trace) != 1 || trace[0].Value != "off" {
		t.Fatalf("trace of %+v", trace)
	}
}

func TestCompleteSlashDescribed(t *testing.T) {
	comps := completeSlash(commands.Default(), "/h")
	var vals []readline.Completion
	(&comps).EachValue(func(c readline.Completion) readline.Completion {
		vals = append(vals, c)
		return c
	})
	if len(vals) == 0 {
		t.Fatal("no completions")
	}
	found := false
	for _, c := range vals {
		if c.Value == "/help" {
			found = true
			if c.Description == "" {
				t.Fatal("help has no description")
			}
			if c.Style != slashStyle {
				t.Fatalf("style %q", c.Style)
			}
		}
	}
	if !found {
		t.Fatalf("no /help in %+v", vals)
	}
	if comps.PREFIX != "/h" {
		t.Fatalf("prefix %q", comps.PREFIX)
	}
}

func TestSlashPrefix(t *testing.T) {
	if slashPrefix("/hel") != "/hel" {
		t.Fatalf("%q", slashPrefix("/hel"))
	}
	if slashPrefix("/copy ") != "" {
		t.Fatalf("%q", slashPrefix("/copy "))
	}
	if slashPrefix("/copy an") != "an" {
		t.Fatalf("%q", slashPrefix("/copy an"))
	}
}

func TestCompleterSlashStartOnly(t *testing.T) {
	fn := Completer(commands.Default(), func() string { return t.TempDir() })
	comps := fn([]rune("say /help"), 9)
	var n int
	(&comps).EachValue(func(c readline.Completion) readline.Completion {
		n++
		return c
	})
	if n != 0 {
		t.Fatalf("mid-line slash should not complete, got %d", n)
	}
}

func TestNavHint(t *testing.T) {
	if !strings.Contains(slashNavHint, "tab complete") || !strings.Contains(slashNavHint, "esc cancel") {
		t.Fatalf("%q", slashNavHint)
	}
}
