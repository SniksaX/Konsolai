package commands

import (
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

func setEnvTree(t *testing.T) *Env {
	t.Helper()
	return &Env{Profile: config.Builtin()}
}

func findChoice(level []Choice, value string) (Choice, bool) {
	for _, c := range level {
		if c.Value == value {
			return c, true
		}
	}
	return Choice{}, false
}

func choiceValues(level []Choice) []string {
	out := make([]string, 0, len(level))
	for _, c := range level {
		if c.Value != "" {
			out = append(out, c.Value)
		}
	}
	return out
}

func TestCommandsWithArgsHaveTrees(t *testing.T) {
	for _, c := range Default().All() {
		if len(c.Args) > 0 && c.Tree == nil {
			t.Errorf("/%s takes arguments and has no tree", c.Name)
		}
	}
}

func TestSetTreeMatchesFriendlyNames(t *testing.T) {
	env := setEnvTree(t)
	tree := Default().Lookup("set").Tree(env)
	got := map[string]Choice{}
	for _, c := range tree {
		if _, dup := got[c.Value]; dup {
			t.Fatalf("duplicate friendly name %s", c.Value)
		}
		got[c.Value] = c
	}
	for _, name := range friendlyNames() {
		ch, ok := got[name]
		if !ok {
			t.Errorf("missing friendly name %s", name)
			continue
		}
		k, ok := config.KeyByPath(friendlyPaths[name])
		if !ok {
			t.Errorf("friendly name %s has no key path", name)
			continue
		}
		if ch.Description != k.Doc {
			t.Errorf("%s description %q want doc %q", name, ch.Description, k.Doc)
		}
		if ch.Current == nil {
			t.Errorf("%s has no current value", name)
			continue
		}
		if ch.Current() == "" {
			t.Errorf("%s current is empty", name)
		}
	}
}

func TestSetTreeEnumsMatchAllowed(t *testing.T) {
	env := setEnvTree(t)
	tree := Default().Lookup("set").Tree(env)
	places := map[string][]string{
		"agent.think_mode":     {"think"},
		"agent.think_answer":   {"think", "answer"},
		"agent.think_lookup":   {"think", "lookup"},
		"agent.think_act":      {"think", "act"},
		"agent.build_thinking": {"think", "build"},
		"agent.approve":        {"approve"},
		"ui.appearance":        {"appearance"},
		"ui.welcome":           {"welcome"},
		"language.mode":        {"language"},
	}
	for _, k := range config.Keys() {
		if len(k.Allowed) == 0 {
			continue
		}
		path, ok := places[k.Path]
		if !ok {
			t.Errorf("allowed key %s is not mapped into the /set tree", k.Path)
			continue
		}
		level := tree
		for _, step := range path {
			ch, found := findChoice(level, step)
			if !found {
				t.Fatalf("%s: missing %s", k.Path, step)
			}
			level = ch.Children()
		}
		got := map[string]bool{}
		for _, v := range choiceValues(level) {
			got[v] = true
		}
		for _, a := range k.Allowed {
			if !got[a] {
				t.Errorf("%s missing allowed %q in %v", k.Path, a, choiceValues(level))
			}
		}
	}
	theme, ok := findChoice(tree, "theme")
	if !ok {
		t.Fatal("no theme")
	}
	got := choiceValues(theme.Children())
	if strings.Join(got, ",") != strings.Join(config.ThemeNames, ",") {
		t.Fatalf("theme values %v want %v", got, config.ThemeNames)
	}
	cur := ""
	for _, c := range theme.Children() {
		if c.Current != nil && c.Current() == "current" {
			cur = c.Value
		}
	}
	if cur != config.Builtin().UI.Theme {
		t.Fatalf("current theme %q", cur)
	}
}

func TestSetStepsIsFreeText(t *testing.T) {
	env := setEnvTree(t)
	tree := Default().Lookup("set").Tree(env)
	steps, ok := findChoice(tree, "steps")
	if !ok {
		t.Fatal("no steps")
	}
	next := steps.Children()
	if len(next) != 1 || next[0].FreeText == nil {
		t.Fatalf("steps level %#v", next)
	}
	ph := next[0].FreeText.Placeholder
	for _, want := range []string{"current 15", "default 15", "range"} {
		if !strings.Contains(ph, want) {
			t.Fatalf("placeholder %q missing %s", ph, want)
		}
	}
}

func TestSetModelTree(t *testing.T) {
	env := setEnvTree(t)
	env.Profile.Models["extra"] = config.Model{Endpoint: "http://localhost:8000/v1", Model: "extra-model"}
	tree := Default().Lookup("set").Tree(env)
	model, ok := findChoice(tree, "model")
	if !ok {
		t.Fatal("no model")
	}
	if strings.Join(choiceValues(model.Children()), ",") != "main,fast,add,remove,discover" {
		t.Fatalf("model level %v", choiceValues(model.Children()))
	}
	fast, _ := findChoice(model.Children(), "fast")
	vals := choiceValues(fast.Children())
	if len(vals) == 0 || vals[0] != "off" {
		t.Fatalf("fast %v", vals)
	}
	joined := strings.Join(vals, ",")
	if !strings.Contains(joined, "default") || !strings.Contains(joined, "extra") {
		t.Fatalf("fast names %v", vals)
	}
	for _, c := range fast.Children() {
		if c.Value == "default" && !strings.Contains(c.Description, "main") {
			t.Fatalf("default roles %q", c.Description)
		}
	}
	rm, _ := findChoice(model.Children(), "remove")
	removed := choiceValues(rm.Children())
	if strings.Join(removed, ",") != "extra" {
		t.Fatalf("remove %v", removed)
	}
	add, _ := findChoice(model.Children(), "add")
	name := add.Children()
	if len(name) != 1 || name[0].FreeText == nil || name[0].FreeText.Placeholder != "<name>" {
		t.Fatalf("add name %#v", name)
	}
	ep := name[0].Children()
	if len(ep) != 1 || ep[0].FreeText == nil || ep[0].FreeText.Placeholder != "<endpoint>" {
		t.Fatalf("add endpoint %#v", ep)
	}
	id := ep[0].Children()
	if len(id) != 1 || id[0].FreeText == nil || id[0].FreeText.Placeholder != "[id]" {
		t.Fatalf("add id %#v", id)
	}
	disc, _ := findChoice(model.Children(), "discover")
	if !disc.Terminal {
		t.Fatal("discover should be terminal")
	}
}

func TestCopyTreeMatchesArgs(t *testing.T) {
	level := Default().Lookup("copy").Tree(nil)
	if strings.Join(choiceValues(level), ",") != "answer,all,code,cmd" {
		t.Fatalf("%v", choiceValues(level))
	}
	for _, c := range level {
		if c.Description == "" {
			t.Fatalf("%s has no description", c.Value)
		}
	}
}

func TestConfigAndRecallTrees(t *testing.T) {
	cfg := Default().Lookup("config").Tree(nil)
	if strings.Join(choiceValues(cfg), ",") != "path,show,validate,diff,init" {
		t.Fatalf("config %v", choiceValues(cfg))
	}
	init, _ := findChoice(cfg, "init")
	if strings.Join(choiceValues(init.Children()), ",") != "--force,--from-current" {
		t.Fatalf("init %v", choiceValues(init.Children()))
	}
	recall := Default().Lookup("recall").Tree(nil)
	if len(recall) != 1 || recall[0].FreeText == nil {
		t.Fatal("recall query")
	}
	scope, ok := findChoice(recall[0].Children(), "--scope")
	if !ok {
		t.Fatal("no --scope")
	}
	if strings.Join(choiceValues(scope.Children()), ",") != "sessions,code,web,all" {
		t.Fatalf("scope %v", choiceValues(scope.Children()))
	}
	forget := Default().Lookup("forget").Tree(nil)
	if strings.Join(choiceValues(forget), ",") != "user,project" {
		t.Fatalf("forget %v", choiceValues(forget))
	}
	cmd := Default().Lookup("cmd").Tree(nil)
	if strings.Join(choiceValues(cmd), ",") != "clear" {
		t.Fatalf("cmd %v", choiceValues(cmd))
	}
}
