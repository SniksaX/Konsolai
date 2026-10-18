package repl

import (
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/commands"
)

func testMenuReg() *commands.Registry {
	return commands.New(
		commands.Command{Name: "help", Usage: "help", Summary: "list commands", Example: "/help"},
		commands.Command{Name: "copy", Usage: "copy answer|all|code|cmd [n]", Summary: "copy last answer",
			Args:    []commands.Arg{{Name: "what", Help: "answer, all, code or cmd", Required: true}, {Name: "n", Help: "how many (max 5)"}},
			Example: "/copy code"},
		commands.Command{Name: "status", Usage: "status", Summary: "show version", Example: "/status"},
		commands.Command{Name: "cd", Usage: "cd <dir>", Summary: "change directory",
			Args:    []commands.Arg{{Name: "dir", Help: "directory to enter", Required: true}},
			Example: "/cd .."},
		commands.Command{Name: "compact", Usage: "compact", Summary: "summarize session"},
		commands.Command{Name: "config", Usage: "config path|show|init|validate", Summary: "yaml config",
			Args: []commands.Arg{{Name: "action", Help: "path, show, init or validate", Required: true}}},
		commands.Command{Name: "exit", Aliases: []string{"quit"}, Usage: "exit", Summary: "leave the repl"},
	)
}

func TestSlashMenuOpensOnSlash(t *testing.T) {
	m := newSlashMenu(testMenuReg())
	m.Sync("/")
	if !m.Open() {
		t.Fatal("expected open")
	}
	if m.Typed() != "/" || m.Selected() != -1 {
		t.Fatalf("typed %q sel %d", m.Typed(), m.Selected())
	}
	if len(m.Items()) != 7 {
		t.Fatalf("items %d", len(m.Items()))
	}
	if m.Items()[0].Name != "help" {
		t.Fatalf("first %s", m.Items()[0].Name)
	}
}

func TestSlashMenuClosesOnSpaceAndNonSlash(t *testing.T) {
	m := newSlashMenu(testMenuReg())
	m.Sync("/")
	m.Sync("/cd ")
	if m.Open() {
		t.Fatal("space should close")
	}
	m.Sync("/")
	if !m.Open() {
		t.Fatal("reopen")
	}
	m.Sync("help")
	if m.Open() {
		t.Fatal("non-slash should close")
	}
}

func TestSlashMenuPrefixThenSubstring(t *testing.T) {
	m := newSlashMenu(testMenuReg())
	m.Sync("/co")
	got := names(m)
	if strings.Join(got, ",") != "copy,compact,config" {
		t.Fatalf("prefix /co %v", got)
	}
	m.Sync("/tat")
	got = names(m)
	if len(got) != 1 || got[0] != "status" {
		t.Fatalf("substring /tat %v", got)
	}
	m.Sync("/HEL")
	got = names(m)
	if len(got) != 1 || got[0] != "help" {
		t.Fatalf("case /HEL %v", got)
	}
	m.Sync("/qui")
	got = names(m)
	if len(got) != 1 || got[0] != "exit" {
		t.Fatalf("alias /qui %v", got)
	}
}

func TestSlashMenuDownUpRestore(t *testing.T) {
	m := newSlashMenu(testMenuReg())
	m.Sync("/")
	r := m.Down()
	if r.Action != actionLine || r.Line != "/help" || m.Selected() != 0 {
		t.Fatalf("down from none %+v sel %d", r, m.Selected())
	}
	r = m.Down()
	if r.Line != "/copy" || m.Selected() != 1 {
		t.Fatalf("second down %+v sel %d", r, m.Selected())
	}
	r = m.Up()
	if r.Line != "/help" || m.Selected() != 0 {
		t.Fatalf("up to first %+v sel %d", r, m.Selected())
	}
	r = m.Up()
	if r.Line != "/" || m.Selected() != -1 {
		t.Fatalf("up restore typed %+v sel %d", r, m.Selected())
	}
}

func TestSlashMenuUpFromNoneSelectsLast(t *testing.T) {
	m := newSlashMenu(testMenuReg())
	m.Sync("/")
	r := m.Up()
	last := m.Items()[len(m.Items())-1].Name
	if r.Line != "/"+last || m.Selected() != len(m.Items())-1 {
		t.Fatalf("up from none %+v sel %d last %s", r, m.Selected(), last)
	}
}

func TestSlashMenuDownWraps(t *testing.T) {
	m := newSlashMenu(testMenuReg())
	m.Sync("/")
	m.Up()
	r := m.Down()
	if r.Line != "/help" || m.Selected() != 0 {
		t.Fatalf("wrap %+v sel %d", r, m.Selected())
	}
}

func TestSlashMenuTypeWhileSelectedResets(t *testing.T) {
	m := newSlashMenu(testMenuReg())
	m.Sync("/")
	m.Down()
	if m.Selected() != 0 {
		t.Fatal(m.Selected())
	}
	m.Sync("/helpx")
	if m.Selected() != -1 || m.Typed() != "/helpx" {
		t.Fatalf("typed reset sel %d typed %q", m.Selected(), m.Typed())
	}
	if m.Detail() {
		t.Fatal("detail should reset")
	}
}

func TestSlashMenuQuestionTogglesDetail(t *testing.T) {
	m := newSlashMenu(testMenuReg())
	m.Sync("/")
	if m.Question().Action != actionPass {
		t.Fatal("? with no selection should pass")
	}
	m.Down()
	r := m.Question()
	if r.Action != actionNone || !m.Detail() {
		t.Fatalf("toggle on %+v detail %v", r, m.Detail())
	}
	if m.Question().Action != actionNone || m.Detail() {
		t.Fatal("toggle off")
	}
}

func TestSlashMenuTabSelectedAndLCP(t *testing.T) {
	m := newSlashMenu(testMenuReg())
	m.Sync("/co")
	r := m.Tab()
	if r.Action != actionLine || r.Line != "/co" {
		t.Fatalf("lcp of copy/compact/config %q", r.Line)
	}
	m.Sync("/comp")
	r = m.Tab()
	if r.Line != "/compact" {
		t.Fatalf("lcp %q", r.Line)
	}
	m.Sync("/")
	m.Down()
	m.Down()
	r = m.Tab()
	if r.Line != "/copy " || m.Open() {
		t.Fatalf("selected copy takes args %q open %v", r.Line, m.Open())
	}
	m.Sync("/")
	m.Down()
	r = m.Tab()
	if r.Line != "/help" || !m.Open() {
		t.Fatalf("help no args %q open %v", r.Line, m.Open())
	}
}

func TestSlashMenuEnterRequiredKeepsEditing(t *testing.T) {
	m := newSlashMenu(testMenuReg())
	m.Sync("/")
	for i := 0; i < len(m.Items()); i++ {
		m.Down()
		if m.Items()[m.Selected()].Name == "cd" {
			break
		}
	}
	if m.Selected() < 0 || m.Items()[m.Selected()].Name != "cd" {
		t.Fatalf("want cd got sel %d", m.Selected())
	}
	r := m.Enter()
	if r.Action != actionLine || r.Line != "/cd " || m.Open() {
		t.Fatalf("required %+v open %v", r, m.Open())
	}
}

func TestSlashMenuEnterSubmitsWithoutRequired(t *testing.T) {
	m := newSlashMenu(testMenuReg())
	m.Sync("/")
	m.Down()
	r := m.Enter()
	if r.Action != actionSubmit || m.Open() {
		t.Fatalf("help submit %+v open %v", r, m.Open())
	}
	m.Sync("/status")
	r = m.Enter()
	if r.Action != actionSubmit {
		t.Fatalf("typed status %+v", r)
	}
}

func TestSlashMenuEscRestoreThenClear(t *testing.T) {
	m := newSlashMenu(testMenuReg())
	m.Sync("/")
	m.Down()
	r := m.Esc()
	if r.Action != actionLine || r.Line != "/" || m.Open() || m.Selected() != -1 {
		t.Fatalf("esc close %+v open %v sel %d", r, m.Open(), m.Selected())
	}
	m.Sync("/")
	if m.Open() {
		t.Fatal("hold should keep menu closed on restored typed")
	}
	r = m.Esc()
	if r.Action != actionClear {
		t.Fatalf("second esc %+v", r)
	}
	m.Sync("/c")
	if !m.Open() {
		t.Fatal("edit after hold should reopen")
	}
}

func TestSlashMenuClosedArrowsPass(t *testing.T) {
	m := newSlashMenu(testMenuReg())
	m.Sync("hello")
	if m.Down().Action != actionPass || m.Up().Action != actionPass {
		t.Fatal("closed arrows must pass")
	}
	if m.Tab().Action != actionPass || m.Enter().Action != actionPass || m.Question().Action != actionPass {
		t.Fatal("closed widgets must pass")
	}
}

func TestSlashMenuDefaultCoOrder(t *testing.T) {
	m := newSlashMenu(commands.Default())
	m.Sync("/co")
	got := names(m)
	if len(got) < 3 || got[0] != "copy" || got[1] != "compact" || got[2] != "config" {
		t.Fatalf("/co default %v", got)
	}
	if !commands.Default().Lookup("cd").HasRequiredArgs() {
		t.Fatal("cd required")
	}
	if commands.Default().Lookup("status").HasRequiredArgs() {
		t.Fatal("status none required")
	}
	if !commands.Default().Lookup("copy").TakesArgs() {
		t.Fatal("copy takes args")
	}
}

func TestSlashMenuRenderWindowAndDetail(t *testing.T) {
	cmds := make([]commands.Command, 0, 12)
	for _, n := range []string{"help", "copy", "history", "show", "resume", "new", "exit", "clear", "cd", "cmd", "status", "config"} {
		c := commands.Command{Name: n, Usage: n, Summary: n + " desc", Example: "/" + n}
		if n == "copy" {
			c.Args = []commands.Arg{{Name: "n", Help: "how many (max 5)"}}
			c.Usage = "copy answer|all|code|cmd [n]"
			c.Example = "/copy code"
		}
		cmds = append(cmds, c)
	}
	m := newSlashMenu(commands.New(cmds...))
	m.Sync("/")
	plain := stripANSI(m.Render(80))
	if strings.Count(plain, "\n")+1 > 9 {
		t.Fatalf("too many rows\n%s", plain)
	}
	if !strings.Contains(plain, "↓ 4 more") {
		t.Fatalf("want clip hint\n%s", plain)
	}
	if strings.Contains(plain, "\x1b[48") || strings.Contains(m.Render(80), "48;") {
		t.Fatal("background color")
	}
	for i := 0; i < 9; i++ {
		m.Down()
	}
	plain = stripANSI(m.Render(80))
	if !strings.Contains(plain, "↑") || !strings.Contains(plain, "more") {
		t.Fatalf("want ↑ more at sel %d\n%s", m.Selected(), plain)
	}
	if !strings.Contains(plain, "› ") {
		t.Fatalf("selected mark\n%s", plain)
	}
	m.sel = -1
	m.typed = "/"
	m.refilter()
	m.open = true
	m.Down()
	m.Down()
	m.Question()
	plain = stripANSI(m.Render(80))
	if !strings.Contains(plain, "usage") || !strings.Contains(plain, "/copy") {
		t.Fatalf("detail usage\n%s", plain)
	}
	if !strings.Contains(plain, "args") || !strings.Contains(plain, "n: how many (max 5)") {
		t.Fatalf("detail args\n%s", plain)
	}
	if !strings.Contains(plain, "example") || !strings.Contains(plain, "/copy code") {
		t.Fatalf("detail example\n%s", plain)
	}
	foot := stripANSI(m.HintFooter())
	for _, w := range []string{"select", "details", "complete", "run", "cancel", "↑↓", "tab", "enter", "esc", "?"} {
		if !strings.Contains(foot, w) {
			t.Fatalf("footer missing %q in %q", w, foot)
		}
	}
}

func names(m *slashMenu) []string {
	var out []string
	for _, c := range m.Items() {
		out = append(out, c.Name)
	}
	return out
}

func stripANSI(s string) string {
	return themeStrip(s)
}

func themeStrip(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) {
					c := s[j]
					j++
					if c >= '@' && c <= '~' {
						break
					}
				}
				i = j
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
