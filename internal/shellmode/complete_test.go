package shellmode

import (
	"context"
	"strings"
	"testing"

	"github.com/reeflective/readline"
)

func TestParseCompgen(t *testing.T) {
	got := ParseCompgen("ls\ncat\n\ncd\n")
	if len(got) != 3 || got[0] != "ls" || got[2] != "cd" {
		t.Fatalf("%v", got)
	}
}

func TestCompleteFirstWordUsesCompgenC(t *testing.T) {
	var scripts []string
	exec := func(_ context.Context, cwd, script string) (string, error) {
		scripts = append(scripts, script)
		if strings.Contains(script, "compgen -c") {
			return "ls\ncat\n", nil
		}
		return "file.txt\n", nil
	}
	comps := completeLine("c", "/tmp", exec)
	if len(scripts) != 1 || !strings.Contains(scripts[0], "compgen -c") {
		t.Fatalf("scripts %v", scripts)
	}
	names := completionValues(comps)
	foundCat, foundCd := false, false
	for _, n := range names {
		if n == "cat" {
			foundCat = true
		}
		if n == "cd" {
			foundCd = true
		}
	}
	if !foundCat || !foundCd {
		t.Fatalf("names %v", names)
	}
}

func TestCompleteAfterCdUsesDirs(t *testing.T) {
	var scripts []string
	exec := func(_ context.Context, cwd, script string) (string, error) {
		scripts = append(scripts, script)
		return "sub\n", nil
	}
	_ = completeLine("cd s", "/tmp", exec)
	if len(scripts) != 1 || !strings.Contains(scripts[0], "compgen -d") {
		t.Fatalf("scripts %v", scripts)
	}
	_ = completeLine("ls f", "/tmp", exec)
	if !strings.Contains(scripts[len(scripts)-1], "compgen -f") {
		t.Fatalf("scripts %v", scripts)
	}
}

func completionValues(c readline.Completions) []string {
	var names []string
	(&c).EachValue(func(v readline.Completion) readline.Completion {
		names = append(names, v.Value)
		return v
	})
	return names
}
