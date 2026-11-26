package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// P0-A scenario 2/3 on the committed P0-B PTY harness: a named folder outside
// the project, plan + approval, real file on disk, and a follow-up turn.

func p0aCreateScenario(t *testing.T, testName, mode, decision string) *ptyScenario {
	t.Helper()
	sc := startPTYScenario(t, testName, mode, false, map[string]string{})
	sc.model.setDecision(decision)
	sc.prompt(t)
	return sc
}

func p0aPlanReply(target string) string {
	plan := `{"summary":"create hello.txt","steps":["write the file"],"files":[` + strconvJSON(target) + `],"commands":[]}`
	return toolCallReply("1", "propose_plan", plan)
}

func p0aCreateAndCheck(t *testing.T, testName, mode, decision string) {
	t.Helper()
	sc := p0aCreateScenario(t, testName, mode, decision)
	named := filepath.Join(sc.home, "named", "hello.txt")
	sc.model.enqueue(
		p0aPlanReply(named),
		toolCallReply("2", "write_file", `{"path":`+strconvJSON(named)+`,"content":"hi"}`),
		contentReply("wrote hello.txt"),
	)
	text := "create a file hello.txt containing hi in " + filepath.Dir(named)
	sc.v.expect("main prompt", screenHas("›"))
	sc.turn(t, text, screenHas("[y]es"))
	if !strings.Contains(sc.v.shot().dump(), "create hello.txt") {
		failScreen(t, "plan was not shown", sc.v.shot(), sc.v.raw())
	}
	sc.approvePlan(t)
	sc.v.expect("done", screenHas("wrote hello.txt"))
	body, err := os.ReadFile(named)
	if err != nil || string(body) != "hi" {
		failScreen(t, "named-folder file missing after approval", sc.v.shot(), sc.v.raw())
	}
	// Follow-up turn in the same session must still work.
	sc.model.setDecision(routeDecisionJSON("act", "low", false))
	sc.model.enqueue(contentReply("FOLLOWUP_OK"))
	sc.turn(t, "say follow up", screenHas("FOLLOWUP_OK"))
	sc.close()
}

func TestP0ACreateFilePlanActOpenAI(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == "p0a-create-act" {
		ptyChildRun()
		return
	}
	t.Setenv("KONSOLAI_PTY_CHILD", "p0a-create-act")
	p0aCreateAndCheck(t, "TestP0ACreateFilePlanActOpenAI", "p0a-create-act", routeDecisionJSON("act", "high", true))
}

func TestP0ACreateFilePlanBuildOpenAI(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == "p0a-create-build" {
		ptyChildRun()
		return
	}
	t.Setenv("KONSOLAI_PTY_CHILD", "p0a-create-build")
	p0aCreateAndCheck(t, "TestP0ACreateFilePlanBuildOpenAI", "p0a-create-build", routeDecisionJSON("build", "high", true))
}
