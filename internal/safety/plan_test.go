package safety

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatchPlanMkdirForTouch(t *testing.T) {
	p := Plan{
		Files: []string{"src/a/.gitkeep", "src/b/.gitkeep"},
		Text:  "create src/a/.gitkeep and src/b/.gitkeep",
	}
	cmd := "mkdir -p src/a src/b && for f in src/a/.gitkeep src/b/.gitkeep; do touch \"$f\"; done"
	ok, hits := MatchPlan(p, cmd)
	if !ok {
		t.Fatalf("want in plan, hits=%v", hits)
	}
	var rules []string
	for _, h := range hits {
		if !h.OK {
			t.Fatalf("miss %s", h.Rule)
		}
		rules = append(rules, h.Rule)
	}
	joined := strings.Join(rules, " | ")
	if !strings.Contains(joined, "R3 mkdir") {
		t.Fatalf("want R3 mkdir, got %s", joined)
	}
	if !strings.Contains(joined, "R3") {
		t.Fatalf("want R3 for-touch, got %s", joined)
	}
}

func TestMatchPlanGitCheckoutAddCommitLog(t *testing.T) {
	msg := "[ADD/MINOR] Add skeleton"
	p := Plan{
		Files:    []string{"src/a/.gitkeep"},
		Commands: []string{"git commit -m '" + msg + "'"},
		Text:     "create branch feat, touch src/a/.gitkeep, git commit -m '" + msg + "'",
	}
	cmd := "git checkout -b feat && touch src/a/.gitkeep && git add src/a/.gitkeep && git commit -m '" + msg + "' && git log -1"
	ok, hits := MatchPlan(p, cmd)
	if !ok {
		t.Fatalf("want in plan, hits=%v", hits)
	}
	if len(hits) != 5 {
		t.Fatalf("hits %d %v", len(hits), hits)
	}
	for i, prefix := range []string{"R4 checkout -b feat", "R3 touch", "R4 git add", "R", "R1 git log -1"} {
		if !hits[i].OK || !strings.HasPrefix(hits[i].Rule, strings.Fields(prefix)[0]) {
			t.Fatalf("hit %d %q want %s", i, hits[i].Rule, prefix)
		}
	}
	if !strings.Contains(hits[3].Rule, "git commit") {
		t.Fatalf("commit %q", hits[3].Rule)
	}
}

func TestMatchPlanMvChmodIntoPlannedDir(t *testing.T) {
	p := Plan{
		Files: []string{"docs/x.md"},
		Text:  "move into docs and chmod",
	}
	cmd := "mv x.md docs/ && chmod 644 docs/x.md"
	ok, hits := MatchPlan(p, cmd)
	if !ok {
		t.Fatalf("want in plan, hits=%v", hits)
	}
	if !strings.HasPrefix(hits[0].Rule, "R3 mv") {
		t.Fatalf("mv %s", hits[0].Rule)
	}
	if !strings.HasPrefix(hits[1].Rule, "R3 chmod") {
		t.Fatalf("chmod %s", hits[1].Rule)
	}
}

func TestMatchPlanVariablePathFails(t *testing.T) {
	p := Plan{
		Files: []string{"src/a/.gitkeep"},
		Text:  "touch planned file",
	}
	ok, hits := MatchPlan(p, `touch "$DEST"`)
	if ok {
		t.Fatalf("variable path must fail, hits=%v", hits)
	}
	ok, _ = MatchPlan(p, "mkdir -p $DIR")
	if ok {
		t.Fatal("mkdir variable must fail")
	}
}

func TestMatchPlanReadOnlyAndDeclared(t *testing.T) {
	cmds := []string{"npm init -y", "npm install express"}
	p := Plan{Commands: cmds, Files: []string{"package.json"}}
	ok, _ := MatchPlan(p, "npm init -y && npm install express")
	if !ok {
		t.Fatal("joined &&")
	}
	ok, _ = MatchPlan(p, "npm init -y && npm install lodash")
	if ok {
		t.Fatal("undeclared")
	}
	ok, _ = MatchPlan(Plan{Commands: []string{`echo "a && b"`}}, `echo "a && b"`)
	if !ok {
		t.Fatal("quoted &&")
	}
	ok, _ = MatchPlan(p, `echo "npm init -y && npm install express"`)
	if ok {
		t.Fatal("quoted must not split")
	}
	ok, hits := MatchPlan(Plan{Commands: []string{"ss -ltnp", "grep 80"}}, "ss -ltnp | grep 80")
	if !ok {
		t.Fatal("read-only pipeline is R1")
	}
	if len(hits) != 1 || !strings.HasPrefix(hits[0].Rule, "R1") {
		t.Fatalf("pipeline hits %v", hits)
	}
	ok, _ = MatchPlan(Plan{Commands: []string{"ss -ltnp | grep 80"}}, "ss -ltnp | grep 80")
	if !ok {
		t.Fatal("declared pipeline")
	}
	ok, _ = MatchPlan(Plan{Commands: []string{"tsc --init"}}, "npx tsc --init")
	if !ok {
		t.Fatal("npx wrapper")
	}
}

func TestMatchPlanNeverGitDangerous(t *testing.T) {
	p := Plan{Files: []string{"a"}, Text: "feat git commit -m x", Commands: []string{"git commit -m x"}}
	for _, cmd := range []string{"git push", "git reset --hard", "git rebase", "git branch -D feat", "git checkout -- a"} {
		ok, _ := MatchPlan(p, cmd)
		if ok {
			t.Fatalf("%s must be outside plan", cmd)
		}
	}
}

func TestAllowPlanFilesLocalBin(t *testing.T) {
	root := t.TempDir()
	g, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	rel := "~/.local/bin/konsolai-plan-auth-test"
	target := filepath.Join(home, ".local", "bin", "konsolai-plan-auth-test")
	if _, err := g.ResolvePath(rel); err == nil {
		t.Fatal("before plan: want escape")
	}
	g.AllowPlanFiles([]string{rel})
	got, err := g.ResolvePath(rel)
	if err != nil {
		t.Fatal(err)
	}
	want, err := evalDeepest(filepath.Clean(target))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if _, err := g.ResolvePath(filepath.Join(home, ".ssh", "id_rsa")); err == nil {
		t.Fatal("ssh must stay denied")
	}
}

func TestPlanCommandForbiddenSudo(t *testing.T) {
	if err := PlanCommandForbidden("sudo pacman -S foo"); err == nil || err.Error() != PrivilegedPlanMsg {
		t.Fatalf("sudo: %v", err)
	}
	if err := PlanCommandForbidden("true && pkexec bash"); err == nil {
		t.Fatal("pkexec")
	}
	if err := PlanCommandForbidden("mkdir -p a/b"); err != nil {
		t.Fatal(err)
	}
}

func TestSplitPlanCommandLoopAndOr(t *testing.T) {
	segs := SplitPlanCommand("mkdir a && for f in x y; do touch $f; done || true")
	if len(segs) != 3 {
		t.Fatalf("%q", segs)
	}
	if !strings.HasPrefix(segs[1], "for f") {
		t.Fatalf("loop %q", segs[1])
	}
	quoted := SplitPlanCommand(`echo "a && b"`)
	if len(quoted) != 1 {
		t.Fatalf("quoted %q", quoted)
	}
}

func TestMatchPlanRedirectAndBrace(t *testing.T) {
	p := Plan{Files: []string{"src/a/.gitkeep", "src/b/.gitkeep"}}
	ok, hits := MatchPlan(p, "mkdir -p src/{a,b} && : > src/a/.gitkeep")
	if !ok {
		t.Fatalf("brace/redirect %v", hits)
	}
}

func TestMatchPlanTraceRuleFormat(t *testing.T) {
	p := Plan{Files: []string{"~/.local/bin/foo"}}
	ok, hits := MatchPlan(p, "mkdir -p ~/.local/bin")
	if !ok || len(hits) != 1 {
		t.Fatalf("%v %v", ok, hits)
	}
	if !strings.HasPrefix(hits[0].Rule, "R3 mkdir") || !strings.Contains(hits[0].Rule, "~/.local/bin") {
		t.Fatalf("rule %q", hits[0].Rule)
	}
}

func TestMatchPlanBenchStepsAndVariants(t *testing.T) {
	commit := Plan{
		Files: []string{".gitignore"},
		Text:  `Create a .gitignore ignoring node_modules/, dist/ and .env, then commit only that file with message "Add gitignore".`,
	}
	ok, hits := MatchPlan(commit, `git add .gitignore && git commit -m "Add gitignore"`)
	if !ok {
		t.Fatalf("git add after commit: %v", hits)
	}
	if !strings.HasPrefix(hits[0].Rule, "R4 git add") {
		t.Fatalf("add %q", hits[0].Rule)
	}
	if !strings.Contains(hits[1].Rule, "git commit") {
		t.Fatalf("commit %q", hits[1].Rule)
	}

	steps := "Rename the five notes\n" +
		"git mv notes/groceries.txt notes/groceries.md\n" +
		"git mv notes/ideas.txt notes/ideas.md\n" +
		"git mv notes/monday.txt notes/monday.md\n" +
		"git mv notes/reading.txt notes/reading.md\n" +
		"git mv notes/tuesday.txt notes/tuesday.md"
	ok, hits = MatchPlan(Plan{Text: steps}, "git mv notes/groceries.txt notes/groceries.md && git mv notes/ideas.txt notes/ideas.md && git mv notes/monday.txt notes/monday.md && git mv notes/reading.txt notes/reading.md && git mv notes/tuesday.txt notes/tuesday.md")
	if !ok {
		t.Fatalf("git mv steps: %v", hits)
	}
	for _, h := range hits {
		if !h.OK || !strings.HasPrefix(h.Rule, "R2 ") {
			t.Fatalf("mv %q", h.Rule)
		}
	}

	node := Plan{Text: "Add a --json flag to cli.js so `node cli.js 1 2 3 --json` prints {\"sum\":6}, keeping plain `6` output without the flag."}
	ok, hits = MatchPlan(node, "node cli.js 1 2 3 --json && node cli.js 1 2 3")
	if !ok {
		t.Fatalf("node variants: %v", hits)
	}
	for _, h := range hits {
		if !strings.HasPrefix(h.Rule, "R2b in plan (variant)") {
			t.Fatalf("variant %q", h.Rule)
		}
	}

	ok, hits = MatchPlan(node, "rm -rf out")
	if ok {
		t.Fatalf("undeclared rm must prompt: %v", hits)
	}
	ok, _ = MatchPlan(Plan{Files: []string{"out"}, Text: "remove build output"}, "rm -rf /tmp/x")
	if ok {
		t.Fatal("rm of a different path must prompt")
	}
	ok, hits = MatchPlan(Plan{Text: "clean\nrm -rf out"}, "rm -rf out")
	if !ok || !strings.HasPrefix(hits[0].Rule, "R2 ") {
		t.Fatalf("declared rm %v", hits)
	}
}
