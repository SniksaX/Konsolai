package repl

import (
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/config"
)

func menuWithSet() *slashMenu {
	m := newSlashMenu(commands.Default())
	m.env = &commands.Env{Profile: config.Builtin()}
	return m
}

func rowValues(m *slashMenu) []string {
	var out []string
	for _, c := range m.rows {
		out = append(out, c.Value)
	}
	return out
}

func TestArgMenuSetListsNames(t *testing.T) {
	m := menuWithSet()
	m.Sync("/set")
	if !m.Open() || !m.arg {
		t.Fatal("set should open the argument level")
	}
	vals := rowValues(m)
	joined := strings.Join(vals, ",")
	for _, name := range []string{"model", "theme", "steps"} {
		if !strings.Contains(joined, name) {
			t.Fatalf("missing %s in %s", name, joined)
		}
	}
	plain := stripANSI(m.Render(120))
	if !strings.Contains(plain, "model") || !strings.Contains(plain, "theme") {
		t.Fatalf("render\n%s", plain)
	}
	if !strings.Contains(plain, "default") || !strings.Contains(plain, "amber") {
		t.Fatalf("current values\n%s", plain)
	}
	for m.Selected() < 0 || m.rows[m.Selected()].Value != "steps" {
		m.Down()
		if m.Selected() < 0 {
			t.Fatal("lost selection")
		}
	}
	plain = stripANSI(m.Render(120))
	if !strings.Contains(plain, "steps") || !strings.Contains(plain, "15") {
		t.Fatalf("steps row\n%s", plain)
	}
}

func TestArgMenuModelThenFast(t *testing.T) {
	m := menuWithSet()
	m.env.Profile.Models["extra"] = config.Model{Endpoint: "http://localhost:8000/v1", Model: "extra-model"}
	m.Sync("/set ")
	var r menuResult
	for i := 0; i < 40; i++ {
		r = m.Down()
		if strings.HasSuffix(r.Line, "model") && !strings.Contains(r.Line, "model ") {
			break
		}
	}
	if r.Line != "/set model" {
		t.Fatalf("down %q", r.Line)
	}
	r = m.Enter()
	if r.Action != actionLine || r.Line != "/set model " {
		t.Fatalf("enter %+v", r)
	}
	m.Sync(r.Line)
	if strings.Join(rowValues(m), ",") != "main,fast,add,remove,discover" {
		t.Fatalf("roles %v", rowValues(m))
	}
	m.Down()
	m.Down()
	r = m.Enter()
	if r.Line != "/set model fast " {
		t.Fatalf("fast %+v", r)
	}
	m.Sync(r.Line)
	vals := rowValues(m)
	if len(vals) == 0 || vals[0] != "off" || !strings.Contains(strings.Join(vals, ","), "extra") {
		t.Fatalf("fast models %v", vals)
	}
	plain := stripANSI(m.Render(100))
	if !strings.Contains(plain, "main") {
		t.Fatalf("roles not shown\n%s", plain)
	}
}

func TestArgMenuStepsFreeText(t *testing.T) {
	m := menuWithSet()
	m.Sync("/set steps ")
	if len(m.rows) != 1 || m.rows[0].FreeText == nil {
		t.Fatalf("rows %v", rowValues(m))
	}
	plain := stripANSI(m.Render(120))
	for _, want := range []string{"current 15", "default 15", "range"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %s\n%s", want, plain)
		}
	}
	m.Sync("/set steps 20")
	r := m.Enter()
	if r.Action != actionLineSubmit || r.Line != "/set steps 20" {
		t.Fatalf("submit %+v", r)
	}
	m.Sync("/set steps x")
	r = m.Enter()
	if r.Action != actionNone || m.errText == "" {
		t.Fatalf("invalid %+v err %q", r, m.errText)
	}
	if !strings.Contains(stripANSI(m.Render(80)), m.errText) {
		t.Fatalf("error not rendered\n%s", stripANSI(m.Render(80)))
	}
}

func TestArgMenuThemeEsc(t *testing.T) {
	m := menuWithSet()
	m.Sync("/set theme ")
	if len(m.rows) != len(config.ThemeNames) {
		t.Fatalf("themes %v", rowValues(m))
	}
	marked := 0
	for _, c := range m.rows {
		if c.Current != nil && c.Current() == "current" {
			marked++
			if c.Value != "amber" {
				t.Fatalf("marked %s", c.Value)
			}
		}
	}
	if marked != 1 {
		t.Fatalf("marked %d", marked)
	}
	r := m.Esc()
	if r.Line != "/set theme" || m.Open() {
		t.Fatalf("esc %+v open %v", r, m.Open())
	}
	m.Sync(r.Line)
	if m.Open() {
		t.Fatal("menu reopened")
	}
	r = m.Esc()
	if r.Action != actionClear {
		t.Fatalf("second %+v", r)
	}
}

func TestArgMenuTabOpensFirst(t *testing.T) {
	m := menuWithSet()
	m.Sync("/set th")
	vals := rowValues(m)
	if strings.Join(vals, ",") != "theme,think" {
		t.Fatalf("filter %v", vals)
	}
	r := m.Tab()
	if r.Action != actionLine || r.Line != "/set theme " {
		t.Fatalf("tab %+v", r)
	}
	m.Sync(r.Line)
	if strings.Join(rowValues(m), ",") != strings.Join(config.ThemeNames, ",") {
		t.Fatalf("opened %v", rowValues(m))
	}
}

func TestArgMenuQuestionDetails(t *testing.T) {
	m := menuWithSet()
	m.Sync("/set ")
	m.Down()
	if m.rows[m.Selected()].Value != "model" {
		t.Fatalf("sel %s", m.rows[m.Selected()].Value)
	}
	if m.Question().Action != actionNone || !m.Detail() {
		t.Fatal("details on")
	}
	plain := stripANSI(m.Render(100))
	for _, want := range []string{"path", "roles.main", "type", "default", "current", "source", "when"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %s\n%s", want, plain)
		}
	}
	m.Question()
	plain = stripANSI(m.Render(100))
	if strings.Contains(plain, "roles.main") {
		t.Fatalf("details stayed\n%s", plain)
	}
}

func TestArgMenuCopy(t *testing.T) {
	m := menuWithSet()
	m.Sync("/copy ")
	if strings.Join(rowValues(m), ",") != "answer,all,code,cmd" {
		t.Fatalf("%v", rowValues(m))
	}
	plain := stripANSI(m.Render(100))
	for _, want := range []string{"answer", "all", "code", "cmd", "Copy the last answer"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %s\n%s", want, plain)
		}
	}
}

func TestArgMenuQuietAfterEsc(t *testing.T) {
	m := menuWithSet()
	m.Sync("/set theme ")
	r := m.Esc()
	m.Sync(r.Line)
	if m.Open() || m.Render(80) != "" {
		t.Fatalf("leftover open %v render %q", m.Open(), m.Render(80))
	}
}
