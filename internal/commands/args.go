package commands

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/SniksaX/Konsolai/internal/config"
)

// FreeText is a level the user types instead of picking a fixed value.
type FreeText struct {
	Placeholder string
	Validate    func(string) error
}

// Choice is one row in a command's argument level.
type Choice struct {
	Value       string
	Description string
	Current     func() string
	Next        []Choice
	Dynamic     func() []Choice
	Terminal    bool
	FreeText    *FreeText
	Detail      func() string
}

// Children is the next level. Dynamic wins when both are set.
func (c Choice) Children() []Choice {
	if c.Dynamic != nil {
		return c.Dynamic()
	}
	return c.Next
}

func (c Choice) hasNext() bool {
	return c.Dynamic != nil || len(c.Next) > 0
}

func envProfile(env *Env) config.Config {
	if env == nil {
		return config.Builtin()
	}
	return env.Profile
}

func nonEmpty(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("enter a value")
	}
	return nil
}

func validN(min, max int) func(string) error {
	return func(s string) error {
		if s == "" {
			return fmt.Errorf("enter a number")
		}
		n := 0
		for _, c := range s {
			if c < '0' || c > '9' {
				return fmt.Errorf("n must be a number")
			}
			n = n*10 + int(c-'0')
		}
		if n < min {
			return fmt.Errorf("n must be at least %d", min)
		}
		if n > max {
			return fmt.Errorf("n must be at most %d", max)
		}
		return nil
	}
}

// friendlyPaths maps each /set name to the key whose doc and current value
// the first level shows. Sub-arguments use their own keys.
var friendlyPaths = map[string]string{
	"model":         "roles.main",
	"embed":         "roles.embed",
	"rerank":        "roles.rerank",
	"database":      "database.url",
	"theme":         "ui.theme",
	"appearance":    "ui.appearance",
	"welcome":       "ui.welcome",
	"banner":        "ui.banner",
	"clear":         "ui.clear_header",
	"pinned_header": "ui.pinned_header",
	"emoji":         "ui.strip_emoji",
	"highlight":     "ui.code_highlight",
	"cursor":        "ui.cursor_color",
	"steps":         "agent.max_steps.act",
	"budget":        "agent.explore_budget.lookup",
	"think":         "agent.think_mode",
	"temperature":   "llm.temperature.answer",
	"top_p":         "llm.top_p",
	"approve":       "agent.approve",
	"plan":          "agent.plan.act_low",
	"trace":         "ui.trace",
	"stats":         "ui.stats",
	"badge":         "ui.badge",
	"language":      "language.mode",
	"languages":     "language.languages",
	"web":           "tools.web.enabled",
	"memory":        "memory.enabled",
	"rag":           "rag.enabled",
	"idle":          "memory.session_idle_minutes",
	"retention":     "memory.retention_days",
	"elide":         "context.elide_trigger",
	"ctx":           "ui.ctx_warn_pct",
	"timeout":       "tools.shell.default_timeout_sec",
}

func friendlyNames() []string {
	var out []string
	for _, line := range strings.Split(config.FriendlyTable(), "\n") {
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "| name") || strings.HasPrefix(line, "| ---") {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) < 2 {
			continue
		}
		name := strings.TrimSpace(f[1])
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func setTree(env *Env) []Choice {
	names := friendlyNames()
	out := make([]Choice, 0, len(names)+1)
	for _, name := range names {
		out = append(out, friendlyChoice(env, name))
	}
	out = append(out, defaultChoice(names))
	return out
}

func defaultChoice(names []string) Choice {
	next := make([]Choice, 0, len(names)+1)
	next = append(next, Choice{
		Value:       "all",
		Description: "Restore every setting to the built-in default",
		Terminal:    true,
	})
	for _, name := range names {
		next = append(next, Choice{
			Value:       name,
			Description: "Restore " + name + " to the built-in default",
			Terminal:    true,
		})
	}
	return Choice{
		Value:       "default",
		Description: "Restore a setting or every setting",
		Next:        next,
	}
}

func friendlyChoice(env *Env, name string) Choice {
	k, _ := config.KeyByPath(friendlyPaths[name])
	ch := Choice{
		Value:       name,
		Description: k.Doc,
		Current:     func() string { return friendlyCurrent(env, name, k) },
		Detail: func() string {
			kk, _ := config.KeyByPath(friendlyPaths[name])
			return keyDetail(env, kk)
		},
	}
	ch.Next = friendlyNext(env, name, k)
	return ch
}

func friendlyCurrent(env *Env, name string, k config.Key) string {
	c := envProfile(env)
	switch name {
	case "emoji":
		if c.StripEmojiOn() {
			return "off"
		}
		return "on"
	case "model":
		if strings.TrimSpace(c.Roles.Main) == "" {
			return "off"
		}
		return c.Roles.Main
	}
	if k.Path == "" {
		return ""
	}
	if k.Kind == config.KindBool {
		if k.GetBool(c) {
			return "on"
		}
		return "off"
	}
	v := strings.TrimSpace(k.Present(c))
	if v == "" || v == `""` {
		return "off"
	}
	if k.Secret {
		return config.MaskURL(v)
	}
	return v
}

func friendlyNext(env *Env, name string, k config.Key) []Choice {
	switch name {
	case "model":
		return modelLevel(env)
	case "steps":
		return []Choice{scalarChoice(env, "agent.max_steps.act", "<n>")}
	case "budget":
		return intentScalars(env, []string{"lookup", "lookup_analysis", "act", "build", "build_docs", "build_analysis"}, "agent.explore_budget.", "<n>")
	case "think":
		return thinkLevel(env)
	case "temperature":
		return temperatureLevel(env)
	case "plan":
		return planLevel(env)
	case "timeout":
		return []Choice{
			subScalar(env, "shell", "tools.shell.default_timeout_sec", "<n>"),
			subScalar(env, "model", "llm.idle_timeout_seconds", "<n>"),
		}
	case "ctx":
		return []Choice{
			subScalar(env, "warn", "ui.ctx_warn_pct", "<n>"),
			subScalar(env, "hot", "ui.ctx_hot_pct", "<n>"),
		}
	case "web":
		out := boolChoices(env, "tools.web.enabled", false)
		out = append(out, subScalar(env, "results", "tools.web.results", "<n>"))
		return out
	case "elide":
		return elideLevel(env)
	case "embed":
		return urlLevel(env, "roles.embed")
	case "rerank":
		return urlLevel(env, "roles.rerank")
	case "database":
		return urlLevel(env, "database.url")
	case "emoji":
		return boolChoices(env, "ui.strip_emoji", true)
	case "theme":
		return enumChoices(env, "ui.theme")
	case "languages":
		return []Choice{textChoice(env, "language.languages", "<list>")}
	default:
		if k.Path == "ui.theme" || len(k.Allowed) > 0 {
			return enumChoices(env, k.Path)
		}
		if k.Kind == config.KindBool {
			return boolChoices(env, k.Path, false)
		}
		token := "<value>"
		switch k.Kind {
		case config.KindInt, config.KindDurationMS:
			token = "<n>"
			return []Choice{scalarChoice(env, k.Path, token)}
		case config.KindFloat:
			return []Choice{scalarChoice(env, k.Path, "<x>")}
		default:
			return []Choice{textChoice(env, k.Path, token)}
		}
	}
}

func valueRange(k config.Key) string {
	switch k.Check {
	case config.CheckPositive:
		return "1+"
	case config.CheckNonNeg:
		return "0+"
	case config.CheckWarnPct:
		return "1-99"
	case config.CheckRatio:
		return "(0,1)"
	case config.CheckRatioClosed:
		return "(0,1]"
	case config.CheckTemp:
		return "(0,2]"
	case config.CheckTopP:
		return "0 or (0,1]"
	default:
		switch k.Kind {
		case config.KindInt, config.KindDurationMS:
			return "1+"
		case config.KindFloat:
			return "a number"
		default:
			return ""
		}
	}
}

func keyDetail(env *Env, k config.Key) string {
	if k.Path == "" {
		return ""
	}
	c := envProfile(env)
	def := config.Builtin()
	var b strings.Builder
	fmt.Fprintf(&b, "path     %s\n", k.Path)
	fmt.Fprintf(&b, "type     %s\n", k.KindName())
	switch {
	case k.Path == "ui.theme":
		fmt.Fprintf(&b, "allowed  %s\n", strings.Join(config.ThemeNames, ", "))
	case len(k.Allowed) > 0:
		fmt.Fprintf(&b, "allowed  %s\n", strings.Join(k.Allowed, ", "))
	default:
		if r := valueRange(k); r != "" {
			fmt.Fprintf(&b, "range    %s\n", r)
		}
	}
	fmt.Fprintf(&b, "default  %s\n", k.Present(def))
	cur := k.Present(c)
	if k.Secret {
		cur = config.MaskURL(cur)
	}
	fmt.Fprintf(&b, "current  %s\n", cur)
	fmt.Fprintf(&b, "source   %s\n", keySource(env, k))
	fmt.Fprintf(&b, "when     %s\n", k.ApplyPhrase())
	return strings.TrimRight(b.String(), "\n")
}

func scalarChoice(env *Env, path, token string) Choice {
	k, _ := config.KeyByPath(path)
	cur := k.Present(envProfile(env))
	def := k.Present(config.Builtin())
	return Choice{
		Terminal:    true,
		Description: k.Doc,
		FreeText: &FreeText{
			Placeholder: fmt.Sprintf("%s  current %s, default %s, range %s", token, cur, def, valueRange(k)),
			Validate: func(s string) error {
				if s == "default" {
					return nil
				}
				cp := envProfile(env)
				return k.SetString(&cp, s)
			},
		},
		Detail: func() string { return keyDetail(env, k) },
	}
}

func textChoice(env *Env, path, token string) Choice {
	k, _ := config.KeyByPath(path)
	cur := k.Present(envProfile(env))
	if k.Secret {
		cur = config.MaskURL(cur)
	}
	def := k.Present(config.Builtin())
	return Choice{
		Terminal:    true,
		Description: k.Doc,
		FreeText: &FreeText{
			Placeholder: fmt.Sprintf("%s  current %s, default %s", token, cur, def),
			Validate: func(s string) error {
				if s == "default" || s == "off" {
					return nil
				}
				cp := envProfile(env)
				return k.SetString(&cp, s)
			},
		},
		Detail: func() string { return keyDetail(env, k) },
	}
}

func enumChoices(env *Env, path string) []Choice {
	k, _ := config.KeyByPath(path)
	vals := k.Allowed
	if path == "ui.theme" {
		vals = config.ThemeNames
	}
	out := make([]Choice, 0, len(vals))
	for _, v := range vals {
		v := v
		out = append(out, Choice{
			Value:       v,
			Description: k.Doc,
			Terminal:    true,
			Current: func() string {
				if strings.EqualFold(strings.Trim(k.Present(envProfile(env)), `"`), v) {
					return "current"
				}
				return ""
			},
			Detail: func() string { return keyDetail(env, k) },
		})
	}
	return out
}

func boolChoices(env *Env, path string, invert bool) []Choice {
	k, _ := config.KeyByPath(path)
	shown := func() string {
		on := false
		if k.GetBool != nil {
			on = k.GetBool(envProfile(env))
		}
		if invert {
			on = !on
		}
		if on {
			return "on"
		}
		return "off"
	}
	var out []Choice
	for _, v := range []string{"on", "off"} {
		v := v
		out = append(out, Choice{
			Value:       v,
			Description: k.Doc,
			Terminal:    true,
			Current: func() string {
				if shown() == v {
					return "current"
				}
				return ""
			},
			Detail: func() string { return keyDetail(env, k) },
		})
	}
	return out
}

func subScalar(env *Env, name, path, token string) Choice {
	k, _ := config.KeyByPath(path)
	return Choice{
		Value:       name,
		Description: k.Doc,
		Next:        []Choice{scalarChoice(env, path, token)},
		Current: func() string {
			kk, _ := config.KeyByPath(path)
			return kk.Present(envProfile(env))
		},
		Detail: func() string { return keyDetail(env, k) },
	}
}

func intentScalars(env *Env, names []string, prefix, token string) []Choice {
	var out []Choice
	for _, name := range names {
		path := prefix + name
		if _, ok := config.KeyByPath(path); !ok {
			continue
		}
		out = append(out, subScalar(env, name, path, token))
	}
	return out
}

func elideLevel(env *Env) []Choice {
	trig := scalarChoice(env, "context.elide_trigger", "<trigger>")
	trig.Terminal = false
	trig.Next = []Choice{scalarChoice(env, "context.elide_target", "<target>")}
	return []Choice{trig}
}

func urlLevel(env *Env, path string) []Choice {
	k, _ := config.KeyByPath(path)
	off := Choice{
		Value:       "off",
		Description: "clear the URL",
		Terminal:    true,
		Current: func() string {
			v := strings.TrimSpace(k.Present(envProfile(env)))
			if v == "" || v == `""` {
				return "current"
			}
			return ""
		},
		Detail: func() string { return keyDetail(env, k) },
	}
	return []Choice{off, textChoice(env, path, "<url>")}
}

func thinkLevel(env *Env) []Choice {
	out := enumChoices(env, "agent.think_mode")
	subs := []struct{ name, path string }{
		{"answer", "agent.think_answer"},
		{"lookup", "agent.think_lookup"},
		{"act", "agent.think_act"},
		{"build", "agent.build_thinking"},
	}
	for _, s := range subs {
		s := s
		k, _ := config.KeyByPath(s.path)
		out = append(out, Choice{
			Value:       s.name,
			Description: k.Doc,
			Next:        enumChoices(env, s.path),
			Detail:      func() string { return keyDetail(env, k) },
		})
	}
	return out
}

func temperatureLevel(env *Env) []Choice {
	direct := scalarChoice(env, "llm.temperature.answer", "<x>")
	out := []Choice{direct}
	for _, name := range []string{"answer", "lookup", "act", "build"} {
		out = append(out, subScalar(env, name, "llm.temperature."+name, "<x>"))
	}
	return out
}

func planLevel(env *Env) []Choice {
	rows := []struct{ name, path string }{
		{"act_low", "agent.plan.act_low"},
		{"act_high", "agent.plan.act_high"},
		{"build", "agent.plan.build"},
	}
	var out []Choice
	for _, r := range rows {
		r := r
		k, _ := config.KeyByPath(r.path)
		out = append(out, Choice{
			Value:       r.name,
			Description: k.Doc,
			Next:        boolChoices(env, r.path, false),
			Current: func() string {
				kk, _ := config.KeyByPath(r.path)
				if kk.GetBool != nil && kk.GetBool(envProfile(env)) {
					return "on"
				}
				return "off"
			},
			Detail: func() string { return keyDetail(env, k) },
		})
	}
	return out
}

func modelLevel(env *Env) []Choice {
	return []Choice{
		{Value: "main", Description: "main chat role", Dynamic: func() []Choice { return modelNameChoices(env, false) }},
		{Value: "fast", Description: "fast chat role", Dynamic: func() []Choice { return modelNameChoices(env, true) }},
		{Value: "add", Description: "add a named model", Next: addModelChoices()},
		{Value: "remove", Description: "remove a model no role uses", Dynamic: func() []Choice { return removableChoices(env) }},
		{Value: "discover", Description: "suggest names from each server", Terminal: true},
	}
}

func modelNameChoices(env *Env, fast bool) []Choice {
	p := envProfile(env)
	var out []Choice
	if fast {
		out = append(out, Choice{
			Value:       "off",
			Description: "leave the fast role unset",
			Terminal:    true,
			Current: func() string {
				if strings.TrimSpace(envProfile(env).Roles.Fast) == "" {
					return "current"
				}
				return ""
			},
		})
	}
	for _, name := range p.ModelNames() {
		name := name
		out = append(out, Choice{
			Value:       name,
			Description: modelRoleText(env, name),
			Terminal:    true,
			Current: func() string {
				cur := envProfile(env)
				hit := cur.Roles.Main == name
				if fast {
					hit = cur.Roles.Fast == name
				}
				if hit {
					return "current"
				}
				return ""
			},
		})
	}
	return out
}

func modelRoleText(env *Env, name string) string {
	p := envProfile(env)
	var roles []string
	if p.Roles.Main == name {
		roles = append(roles, "main")
	}
	if p.Roles.Fast == name {
		roles = append(roles, "fast")
	}
	if len(roles) == 0 {
		return "unused"
	}
	return strings.Join(roles, ", ")
}

func removableChoices(env *Env) []Choice {
	p := envProfile(env)
	var out []Choice
	for _, name := range p.ModelNames() {
		if name == p.Roles.Main || name == p.Roles.Fast {
			continue
		}
		name := name
		out = append(out, Choice{Value: name, Description: "not used by a role", Terminal: true})
	}
	if len(out) == 0 {
		return []Choice{{
			FreeText: &FreeText{
				Placeholder: "no unused models",
				Validate:    func(string) error { return fmt.Errorf("every model is in use") },
			},
		}}
	}
	return out
}

func addModelChoices() []Choice {
	return []Choice{{
		Description: "new model name",
		FreeText:    &FreeText{Placeholder: "<name>", Validate: nonEmpty},
		Next: []Choice{{
			Description: "OpenAI-compatible endpoint",
			FreeText:    &FreeText{Placeholder: "<endpoint>", Validate: nonEmpty},
			Terminal:    true,
			Next: []Choice{{
				Description: "server model id",
				FreeText:    &FreeText{Placeholder: "[id]"},
				Terminal:    true,
			}},
		}},
	}}
}

func copyTree(*Env) []Choice {
	n := Choice{
		Terminal:    true,
		Description: "how many (max 5)",
		FreeText:    &FreeText{Placeholder: "<n>  how many, max 5", Validate: validN(1, 5)},
	}
	kind := func(v, d string, withN bool) Choice {
		c := Choice{Value: v, Description: d, Terminal: true}
		if withN {
			c.Next = []Choice{n}
		}
		return c
	}
	return []Choice{
		kind("answer", "Copy the last answer", true),
		kind("all", "Copy the last prompt and answer", true),
		kind("code", "Copy a code block from the last answer", true),
		kind("cmd", "Copy a command from the last answer", false),
	}
}

func nTree(placeholder string, min, max int) func(*Env) []Choice {
	return func(*Env) []Choice {
		return []Choice{{
			Terminal:    true,
			Description: placeholder,
			FreeText:    &FreeText{Placeholder: placeholder, Validate: validN(min, max)},
		}}
	}
}

func resumeTree(*Env) []Choice {
	return []Choice{{
		Description: "session index",
		FreeText:    &FreeText{Placeholder: "<n>", Validate: validN(1, 50)},
		Next: []Choice{{
			Terminal:    true,
			Description: "the next prompt",
			FreeText:    &FreeText{Placeholder: "<request>", Validate: nonEmpty},
		}},
	}}
}

func cdTree(env *Env) []Choice {
	cwd := "."
	if env != nil && env.Cwd != "" {
		cwd = env.Cwd
	}
	out := []Choice{{Value: "..", Description: "parent directory", Terminal: true}}
	ents, err := os.ReadDir(cwd)
	if err != nil {
		out = append(out, Choice{
			Terminal:    true,
			Description: "directory to enter",
			FreeText:    &FreeText{Placeholder: "<dir>", Validate: nonEmpty},
		})
		return out
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		name := name
		out = append(out, Choice{Value: name, Description: "directory", Terminal: true})
	}
	return out
}

func cmdTree(*Env) []Choice {
	return []Choice{
		{Value: "clear", Description: "clear the screen", Terminal: true},
		{Value: "forget", Description: "drop the shell context", Terminal: true},
	}
}

func onOffTree(onDesc, offDesc string) func(*Env) []Choice {
	return func(*Env) []Choice {
		return []Choice{
			{Value: "on", Description: onDesc, Terminal: true},
			{Value: "off", Description: offDesc, Terminal: true},
		}
	}
}

func thinkCmdTree(*Env) []Choice {
	return []Choice{
		{Value: "on", Description: "always think", Terminal: true},
		{Value: "off", Description: "never think", Terminal: true},
		{Value: "auto", Description: "think when the intent asks for it", Terminal: true},
	}
}

func configTree(*Env) []Choice {
	term := func(v, d string) Choice {
		return Choice{Value: v, Description: d, Terminal: true}
	}
	return []Choice{
		term("path", "print the config path"),
		term("show", "print the active settings"),
		term("validate", "load and validate the file"),
		term("diff", "show changes from the built-in defaults"),
		{
			Value: "init", Description: "write a sample config", Terminal: true,
			Next: []Choice{
				term("--force", "overwrite an existing file"),
				term("--from-current", "write the current settings"),
			},
		},
	}
}

func themeCmdTree(env *Env) []Choice { return enumChoices(env, "ui.theme") }

func indexTree(*Env) []Choice {
	return []Choice{
		{Value: "status", Description: "show index status", Terminal: true},
		{Value: "clear", Description: "drop indexed chunks", Terminal: true},
		{
			Terminal: true, Description: "index only this path",
			FreeText: &FreeText{Placeholder: "<path>", Validate: nonEmpty},
		},
	}
}

func recallTree(*Env) []Choice {
	scopes := []Choice{
		{Value: "sessions", Description: "search session memory", Terminal: true},
		{Value: "code", Description: "search indexed code", Terminal: true},
		{Value: "web", Description: "search fetched pages", Terminal: true},
		{Value: "all", Description: "search every scope", Terminal: true},
	}
	return []Choice{{
		Terminal:    true,
		Description: "search text",
		FreeText:    &FreeText{Placeholder: "<query>", Validate: nonEmpty},
		Next: []Choice{{
			Value: "--scope", Description: "limit the search", Next: scopes,
		}},
	}}
}

func rememberTree(*Env) []Choice {
	fact := Choice{
		Terminal: true, Description: "the fact to store",
		FreeText: &FreeText{Placeholder: "<fact>", Validate: nonEmpty},
	}
	return []Choice{
		{Value: "--project", Description: "store the fact on this project", Next: []Choice{fact}},
		fact,
	}
}

func forgetTree(env *Env) []Choice {
	return []Choice{
		{Value: "user", Description: "delete a user fact", Dynamic: func() []Choice { return factChoices(env, "user") }},
		{Value: "project", Description: "delete a project fact", Dynamic: func() []Choice { return factChoices(env, "project") }},
	}
}

func factChoices(env *Env, scope string) []Choice {
	id := Choice{
		Terminal: true, Description: "fact id",
		FreeText: &FreeText{Placeholder: "<id>", Validate: validN(1, 1<<30)},
	}
	if env == nil || env.Mem == nil || env.Mem.Store() == nil {
		return []Choice{id}
	}
	ctx := context.Background()
	user, proj, err := env.Mem.Store().ListFacts(ctx, env.Profile.Name, projectIDOf(ctx, env))
	if err != nil {
		return []Choice{id}
	}
	rows := user
	if scope == "project" {
		rows = proj
	}
	if len(rows) == 0 {
		return []Choice{id}
	}
	out := make([]Choice, 0, len(rows))
	for _, f := range rows {
		out = append(out, Choice{
			Value: strconv.FormatInt(f.ID, 10), Description: f.Fact, Terminal: true,
		})
	}
	return out
}

func statsTree(*Env) []Choice {
	return []Choice{
		{Value: "today", Description: "calls since local midnight", Terminal: true},
		{Value: "week", Description: "calls in the last seven days", Terminal: true},
		{Value: "all", Description: "every recorded call", Terminal: true},
	}
}

func fileTree(*Env) []Choice {
	return []Choice{{
		Terminal: true, Description: "output path",
		FreeText: &FreeText{Placeholder: "<file>"},
		Next: []Choice{{
			Value: "--force", Description: "overwrite an existing file", Terminal: true,
		}},
	}}
}
