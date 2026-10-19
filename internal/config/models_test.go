package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveConfigPathOrder(t *testing.T) {
	t.Setenv("KONSOLAI_CONFIG", filepath.Join(t.TempDir(), "from-env.yaml"))
	if got := ResolveConfigPath(""); got != os.Getenv("KONSOLAI_CONFIG") {
		t.Fatalf("env %s", got)
	}
	if got := ResolveConfigPath("/flag.yaml"); got != "/flag.yaml" {
		t.Fatalf("flag %s", got)
	}
	t.Setenv("KONSOLAI_CONFIG", "")
	if got := ResolveConfigPath(""); got != DefaultPath() {
		t.Fatalf("default %s", got)
	}
}

func TestSymlinkWriteKeepsLink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.yaml")
	link := filepath.Join(dir, "link.yaml")
	body := "defaults:\n  ui:\n    theme: amber   # palette\n"
	if err := os.WriteFile(target, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := SetFileKey(link, "ui.theme", "jade", false); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("link was replaced")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "jade") || !strings.Contains(string(got), "palette") {
		t.Fatalf("target\n%s", got)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
}

func TestMissingModelListsNames(t *testing.T) {
	clearEnv(t)
	raw := "defaults:\n  roles:\n    main: missing\n"
	f, err := Parse([]byte(raw), "m.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Resolve("")
	if err == nil || !strings.Contains(err.Error(), "missing") || !strings.Contains(err.Error(), "default") {
		t.Fatalf("%v", err)
	}
}

func TestLegacyModelMigration(t *testing.T) {
	clearEnv(t)
	raw := "defaults:\n  llm:\n    endpoint: http://legacy/v1\n    model: alpha\n    thinking_kwargs: true\n  fast:\n    enabled: true\n    endpoint: http://fast/v1\n    model: beta\n  rag:\n    embed_endpoint: http://embed\n    rerank_endpoint: http://rerank\n"
	f, err := Parse([]byte(raw), "old.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if c.MainEndpoint() != "http://legacy/v1" || c.MainModelID() != "alpha" || !c.MainThinking() {
		t.Fatalf("main %+v", c.Main())
	}
	if c.Roles.Fast != "fast" || c.FastEndpoint() != "http://fast/v1" || c.FastModelID() != "beta" {
		t.Fatalf("fast %+v role %q", c.Models["fast"], c.Roles.Fast)
	}
	if c.EmbedURL() != "http://embed" || c.RerankURL() != "http://rerank" {
		t.Fatalf("embed %q rerank %q", c.EmbedURL(), c.RerankURL())
	}
}

func TestNewBehaviorKeys(t *testing.T) {
	c := Builtin()
	if c.TemperatureFor("answer") != 0.3 || c.TemperatureFor("build") != 0.3 || c.TemperatureFor("lookup") != 0.2 || c.TemperatureFor("act") != 0.2 {
		t.Fatalf("temp %+v", c.LLM.Temperature)
	}
	if c.LLM.TopP != 0 || c.LLM.Retries != 1 || c.LLM.RetryWaitMS != 2000 {
		t.Fatalf("sample %+v", c.LLM)
	}
	if c.Agent.ThinkMode != "auto" || c.AnswerThinking() != "auto" || c.LookupThinking() != "auto" || c.ActThinking() != "plan_step" || c.BuildThink() != "plan_step" {
		t.Fatalf("think %+v", c.Agent)
	}
	if c.Agent.Approve != "ask" || c.NeedPlan("build", false) != true || c.NeedPlan("act", true) != true || c.NeedPlan("act", false) {
		t.Fatalf("approve %q plan %+v", c.Agent.Approve, c.Agent.Plan)
	}
	if c.UI.Trace || !c.ShowStatsLine() || !c.ShowBadge() || !c.WebOn() || c.Tools.Web.Results != 0 {
		t.Fatal("display")
	}
	if c.Language.Mode != "auto" || !c.MemoryOn() {
		t.Fatal("mode")
	}
	c.Agent.Plan.Build = false
	if c.NeedPlan("build", false) {
		t.Fatal("build plan stayed on")
	}
	c.Tools.Web.Enabled = false
	if c.WebOn() {
		t.Fatal("web stayed on")
	}
	c.UI.Stats = false
	c.UI.Theme = "amber"
	if c.ShowStatsLine() {
		t.Fatal("stats stayed on")
	}
}
