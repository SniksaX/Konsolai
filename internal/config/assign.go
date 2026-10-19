package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Keys returns a copy of the setting table.
func Keys() []Key {
	out := make([]Key, len(keyTable))
	copy(out, keyTable)
	return out
}

// KeyByPath returns the leaf for an exact path.
func KeyByPath(path string) (Key, bool) {
	for i := range keyTable {
		if keyTable[i].Path == path {
			return keyTable[i], true
		}
	}
	return Key{}, false
}

// MatchKey resolves a /set name: an exact path, an exact friendly name,
// or a unique suffix. Several matches come back as candidates.
func MatchKey(query string) (Key, []Key, bool) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return Key{}, nil, false
	}
	if k, ok := KeyByPath(q); ok {
		return k, nil, true
	}
	var named []Key
	for i := range keyTable {
		if strings.EqualFold(keyTable[i].Name, q) {
			named = append(named, keyTable[i])
		}
	}
	if len(named) == 1 {
		return named[0], nil, true
	}
	if len(named) > 1 {
		return Key{}, named, false
	}
	var suffix []Key
	for i := range keyTable {
		k := keyTable[i]
		last := k.Path
		if i := strings.LastIndex(k.Path, "."); i >= 0 {
			last = k.Path[i+1:]
		}
		if strings.EqualFold(last, q) || strings.HasSuffix(strings.ToLower(k.Path), "."+q) {
			suffix = append(suffix, k)
		}
	}
	if len(suffix) == 1 {
		return suffix[0], nil, true
	}
	if len(suffix) > 1 {
		return Key{}, suffix, false
	}
	return Key{}, nil, false
}

// SetString validates raw and stores it on c.
func (k Key) SetString(c *Config, raw string) error {
	norm, err := k.Normalize(raw)
	if err != nil {
		return err
	}
	if err := k.setFromString(c, norm); err != nil {
		return err
	}
	if err := k.validate(*c); err != nil {
		return err
	}
	return validateConfig(*c)
}

// Normalize turns a /set token into the stored scalar.
func (k Key) Normalize(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	kind := k.kindName()
	switch k.Kind {
	case KindBool:
		switch strings.ToLower(raw) {
		case "on", "true", "yes":
			return "true", nil
		case "off", "false", "no":
			return "false", nil
		default:
			return "", fmt.Errorf("%s is a bool; allowed: on, off", k.label())
		}
	case KindEnum, KindString:
		if k.Path == "ui.theme" {
			if name, ok := ResolveTheme(raw); ok {
				return name, nil
			}
			return "", fmt.Errorf("%s is an enum; allowed: %s", k.label(), strings.Join(ThemeNames, ", "))
		}
		if len(k.Allowed) > 0 {
			for _, a := range k.Allowed {
				if strings.EqualFold(raw, a) {
					return a, nil
				}
			}
			return "", fmt.Errorf("%s is an enum; allowed: %s", k.label(), strings.Join(k.Allowed, ", "))
		}
		return raw, nil
	case KindInt, KindDurationMS:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return "", fmt.Errorf("%s is a %s; allowed: an integer", k.label(), kind)
		}
		return strconv.Itoa(n), nil
	case KindFloat:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return "", fmt.Errorf("%s is a float; allowed: a number", k.label())
		}
		return strconv.FormatFloat(f, 'g', -1, 64), nil
	case KindList:
		return raw, nil
	default:
		return "", fmt.Errorf("%s is a %s", k.label(), kind)
	}
}

func (k Key) label() string {
	if k.Name != "" && !strings.Contains(k.Name, " ") {
		return k.Name
	}
	return k.Path
}

// KindName is string, bool, int, float, duration_ms, list, or enum.
func (k Key) KindName() string { return k.kindName() }

func (k Key) kindName() string {
	switch k.Kind {
	case KindBool:
		return "bool"
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindDurationMS:
		return "duration_ms"
	case KindList:
		return "list"
	case KindEnum:
		return "enum"
	default:
		return "string"
	}
}

// Present is the scalar written back to the file.
func (k Key) Present(c Config) string { return k.present(c, false) }

// ApplyPhrase is the /set confirmation.
func (k Key) ApplyPhrase() string {
	switch k.Apply {
	case ApplyNext:
		return "from the next turn"
	case ApplyRestart:
		return "after restart"
	default:
		return "applied now"
	}
}

// ResetDefault copies the built-in value of this key onto c.
func (k Key) ResetDefault(c *Config) {
	b := Builtin()
	switch k.Kind {
	case KindString, KindEnum:
		k.SetStr(c, k.GetStr(b))
	case KindBool:
		k.SetBool(c, k.GetBool(b))
	case KindInt, KindDurationMS:
		k.SetInt(c, k.GetInt(b))
	case KindFloat:
		k.SetFloat(c, k.GetFloat(b))
	case KindList:
		k.SetList(c, copyList(k.GetList(b)))
	}
}

// EnvOverride reports the environment variable that overrides this key, if set.
func (k Key) EnvOverride() (string, bool) {
	if k.Env == "" {
		return "", false
	}
	v, ok := os.LookupEnv(k.Env)
	return k.Env, ok && strings.TrimSpace(v) != ""
}

// FriendlyTable is the README table of friendly /set names.
func FriendlyTable() string {
	type row struct{ name, values, keys string }
	rows := []row{
		{"model", "main <name> | fast <name>|off | add <name> <endpoint> [id] | remove <name> | discover", "models, roles.main, roles.fast"},
		{"embed", "<url>|off", "roles.embed"},
		{"rerank", "<url>|off", "roles.rerank"},
		{"database", "<url>|off", "database.url"},
		{"theme", "amber | glacier | nebula | jade | rose | mono", "ui.theme"},
		{"appearance", "auto | dark | light", "ui.appearance"},
		{"welcome", "split | frame | minimal", "ui.welcome"},
		{"banner", "on | off", "ui.banner"},
		{"emoji", "on | off", "ui.strip_emoji"},
		{"highlight", "on | off", "ui.code_highlight"},
		{"cursor", "on | off", "ui.cursor_color"},
		{"steps", "<n> | lookup|act|build <n>", "agent.max_steps.lookup, agent.max_steps.act, agent.max_steps.build"},
		{"budget", "<intent> <n>", "agent.explore_budget.*"},
		{"think", "on|off|auto | <intent> on|off|plan_step|until_plan", "agent.think_mode, agent.think_answer, agent.think_lookup, agent.think_act, agent.build_thinking"},
		{"temperature", "<x> [intent]", "llm.temperature.answer, llm.temperature.lookup, llm.temperature.act, llm.temperature.build"},
		{"top_p", "<x>", "llm.top_p"},
		{"approve", "ask | in_plan | all_safe", "agent.approve"},
		{"plan", "act_low|act_high|build on|off", "agent.plan.act_low, agent.plan.act_high, agent.plan.build"},
		{"trace", "on | off", "ui.trace"},
		{"stats", "on | off", "ui.stats"},
		{"badge", "on | off", "ui.badge"},
		{"language", "auto | en | fr", "language.mode"},
		{"languages", "<list>", "language.languages"},
		{"web", "on|off | results <n>", "tools.web.enabled, tools.web.results"},
		{"memory", "on | off", "memory.enabled"},
		{"rag", "on | off", "rag.enabled"},
		{"idle", "<minutes>", "memory.session_idle_minutes"},
		{"retention", "<days>", "memory.retention_days"},
		{"elide", "<trigger> <target>", "context.elide_trigger, context.elide_target"},
		{"ctx", "warn|hot <pct>", "ui.ctx_warn_pct, ui.ctx_hot_pct"},
		{"timeout", "shell <sec> | model <sec>", "tools.shell.default_timeout_sec, llm.idle_timeout_seconds"},
	}
	var b strings.Builder
	b.WriteString("| name | values | keys |\n| --- | --- | --- |\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", r.name, r.values, r.keys)
	}
	return b.String()
}

// CompleteSet returns tab candidates for /set.
func CompleteSet(parts []string, partial string, haveSpace bool) []string {
	names := friendlyCompletions()
	if len(parts) <= 1 && !haveSpace {
		return filterPrefix(names, partial)
	}
	head := ""
	if len(parts) > 0 {
		head = parts[0]
	}
	switch head {
	case "default":
		return filterPrefix(append([]string{"all"}, friendlyTableNames()...), partial)
	case "theme":
		return filterPrefix(ThemeNames, partial)
	case "appearance":
		return filterPrefix([]string{"auto", "dark", "light"}, partial)
	case "welcome":
		return filterPrefix([]string{"split", "frame", "minimal"}, partial)
	case "approve":
		return filterPrefix([]string{"ask", "in_plan", "all_safe"}, partial)
	case "think":
		if len(parts) == 1 || (len(parts) == 2 && !haveSpace) {
			return filterPrefix([]string{"on", "off", "auto", "answer", "lookup", "act", "build"}, partial)
		}
		return filterPrefix([]string{"on", "off", "auto", "plan_step", "until_plan"}, partial)
	case "model":
		if len(parts) == 1 || (len(parts) == 2 && !haveSpace) {
			return filterPrefix([]string{"main", "fast", "add", "remove", "discover"}, partial)
		}
		return nil
	case "steps":
		if len(parts) == 1 || (len(parts) == 2 && !haveSpace) {
			return filterPrefix([]string{"lookup", "act", "build"}, partial)
		}
		return nil
	case "plan":
		if len(parts) == 1 || (len(parts) == 2 && !haveSpace) {
			return filterPrefix([]string{"act_low", "act_high", "build"}, partial)
		}
		return filterPrefix([]string{"on", "off"}, partial)
	case "timeout":
		if len(parts) == 1 || (len(parts) == 2 && !haveSpace) {
			return filterPrefix([]string{"shell", "model"}, partial)
		}
		return nil
	case "ctx":
		if len(parts) == 1 || (len(parts) == 2 && !haveSpace) {
			return filterPrefix([]string{"warn", "hot"}, partial)
		}
		return nil
	case "web":
		return filterPrefix([]string{"on", "off", "results"}, partial)
	default:
		if k, _, ok := MatchKey(head); ok {
			if len(k.Allowed) > 0 {
				return filterPrefix(k.Allowed, partial)
			}
			if k.Kind == KindBool {
				return filterPrefix([]string{"on", "off", "default"}, partial)
			}
		}
		return filterPrefix(names, partial)
	}
}

func friendlyTableNames() []string {
	var out []string
	for _, line := range strings.Split(FriendlyTable(), "\n") {
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

func friendlyCompletions() []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	add("default")
	for _, line := range strings.Split(FriendlyTable(), "\n") {
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "| name") || strings.HasPrefix(line, "| ---") {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) > 1 {
			add(strings.TrimSpace(f[1]))
		}
	}
	for i := range keyTable {
		add(keyTable[i].Path)
		if keyTable[i].Name != "" {
			add(keyTable[i].Name)
		}
	}
	return out
}

func filterPrefix(items []string, partial string) []string {
	partial = strings.ToLower(partial)
	var out []string
	for _, s := range items {
		if partial == "" || strings.HasPrefix(strings.ToLower(s), partial) {
			out = append(out, s)
		}
	}
	return out
}
