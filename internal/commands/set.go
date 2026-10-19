package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/turn"
	"github.com/SniksaX/Konsolai/internal/ui"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
)

func runModels(ctx context.Context, env *Env, args []string) int {
	fmt.Fprintln(env.out(), ui.Current().Muted("now: /set model"))
	return runSet(ctx, env, append([]string{"model"}, args...))
}

func runSet(ctx context.Context, env *Env, args []string) int {
	session := false
	rest := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--session" {
			session = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) == 0 {
		if env != nil && env.InRepl && pickerInteractive() {
			return runSetMenu(ctx, env, session)
		}
		fmt.Fprint(env.out(), formatSetList(env.Profile))
		return 0
	}
	switch rest[0] {
	case "model":
		return setModel(ctx, env, rest[1:], session)
	case "steps":
		return setSteps(env, rest[1:], session)
	case "budget":
		return setBudget(env, rest[1:], session)
	case "think":
		return setThink(env, rest[1:], session)
	case "temperature":
		return setTemperature(env, rest[1:], session)
	case "top_p":
		return setOne(env, "llm.top_p", rest[1:], session)
	case "approve":
		return setOne(env, "agent.approve", rest[1:], session)
	case "plan":
		return setPlan(env, rest[1:], session)
	case "trace":
		return setBoolKey(env, "ui.trace", rest[1:], session)
	case "stats":
		return setBoolKey(env, "ui.stats", rest[1:], session)
	case "badge":
		return setBoolKey(env, "ui.badge", rest[1:], session)
	case "banner":
		return setBoolKey(env, "ui.banner", rest[1:], session)
	case "highlight":
		return setBoolKey(env, "ui.code_highlight", rest[1:], session)
	case "cursor":
		return setBoolKey(env, "ui.cursor_color", rest[1:], session)
	case "emoji":
		return setEmoji(env, rest[1:], session)
	case "memory":
		return setBoolKey(env, "memory.enabled", rest[1:], session)
	case "rag":
		return setBoolKey(env, "rag.enabled", rest[1:], session)
	case "web":
		return setWeb(env, rest[1:], session)
	case "language":
		return setOne(env, "language.mode", rest[1:], session)
	case "languages":
		return setOne(env, "language.languages", rest[1:], session)
	case "idle":
		return setOne(env, "memory.session_idle_minutes", rest[1:], session)
	case "retention":
		return setOne(env, "memory.retention_days", rest[1:], session)
	case "elide":
		return setElide(env, rest[1:], session)
	case "ctx":
		return setCtx(env, rest[1:], session)
	case "timeout":
		return setTimeout(env, rest[1:], session)
	case "embed":
		return setURLRole(env, "roles.embed", rest[1:], session)
	case "rerank":
		return setURLRole(env, "roles.rerank", rest[1:], session)
	case "database":
		return setDatabase(env, rest[1:], session)
	case "theme":
		return setTheme(env, rest[1:], session)
	case "appearance":
		return setOne(env, "ui.appearance", rest[1:], session)
	case "welcome":
		return setOne(env, "ui.welcome", rest[1:], session)
	}
	k, cands, ok := config.MatchKey(rest[0])
	if !ok {
		if len(cands) > 0 {
			fmt.Fprint(env.out(), formatCandidates(cands))
			return 2
		}
		fmt.Fprintf(env.errw(), "unknown setting %q; try /set\n", rest[0])
		return 2
	}
	if k.Secret {
		fmt.Fprintf(env.errw(), "%s is a secret; set it with ~/.pgpass or the environment\n", k.Path)
		return 2
	}
	if len(rest) == 1 {
		if k.Path == "ui.theme" && env != nil && env.InRepl && pickerInteractive() {
			return runThemePicker(env)
		}
		fmt.Fprint(env.out(), describeKey(env, k))
		return 0
	}
	if rest[1] == "default" {
		return commitKey(env, k, "", session, true)
	}
	return commitKey(env, k, strings.Join(rest[1:], " "), session, false)
}

func setOne(env *Env, path string, args []string, session bool) int {
	k, ok := config.KeyByPath(path)
	if !ok {
		fmt.Fprintln(env.errw(), "unknown setting")
		return 2
	}
	if len(args) == 0 {
		fmt.Fprint(env.out(), describeKey(env, k))
		return 0
	}
	if args[0] == "default" {
		return commitKey(env, k, "", session, true)
	}
	return commitKey(env, k, strings.Join(args, " "), session, false)
}

func setBoolKey(env *Env, path string, args []string, session bool) int {
	return setOne(env, path, args, session)
}

func setEmoji(env *Env, args []string, session bool) int {
	k, _ := config.KeyByPath("ui.strip_emoji")
	if len(args) == 0 {
		on := "off"
		if !env.Profile.StripEmojiOn() {
			on = "on"
		}
		fmt.Fprintf(env.out(), "emoji %s\n%s", on, describeKey(env, k))
		return 0
	}
	if args[0] == "default" {
		return commitKey(env, k, "", session, true)
	}
	switch strings.ToLower(args[0]) {
	case "on":
		return commitKey(env, k, "false", session, false)
	case "off":
		return commitKey(env, k, "true", session, false)
	default:
		fmt.Fprintln(env.errw(), "emoji is a bool; allowed: on, off")
		return 2
	}
}

func setTheme(env *Env, args []string, session bool) int {
	if len(args) == 0 {
		if env != nil && env.InRepl && pickerInteractive() {
			return runThemePicker(env)
		}
		k, _ := config.KeyByPath("ui.theme")
		fmt.Fprint(env.out(), describeKey(env, k))
		return 0
	}
	return setOne(env, "ui.theme", args, session)
}

func setSteps(env *Env, args []string, session bool) int {
	if len(args) == 0 {
		fmt.Fprintf(env.out(), "lookup %d\nact %d\nbuild %d\n", env.Profile.Agent.MaxSteps.Lookup, env.Profile.Agent.MaxSteps.Act, env.Profile.Agent.MaxSteps.Build)
		return 0
	}
	if len(args) == 1 {
		return setOne(env, "agent.max_steps.act", args, session)
	}
	path := "agent.max_steps." + strings.ToLower(args[0])
	if _, ok := config.KeyByPath(path); !ok {
		fmt.Fprintln(env.errw(), "steps is an int; allowed: lookup, act, build")
		return 2
	}
	return setOne(env, path, args[1:], session)
}

func setBudget(env *Env, args []string, session bool) int {
	if len(args) < 2 {
		fmt.Fprintln(env.errw(), "usage: /set budget <intent> <n>")
		return 2
	}
	name := strings.ToLower(args[0])
	switch name {
	case "lookup", "act", "build", "lookup_analysis", "build_docs", "build_analysis":
	default:
		fmt.Fprintln(env.errw(), "budget is an int; allowed: lookup, lookup_analysis, act, build, build_docs, build_analysis")
		return 2
	}
	return setOne(env, "agent.explore_budget."+name, args[1:], session)
}

func setThink(env *Env, args []string, session bool) int {
	if len(args) == 0 {
		fmt.Fprintf(env.out(), "think %s\nanswer %s\nlookup %s\nact %s\nbuild %s\n",
			env.Profile.Agent.ThinkMode, env.Profile.AnswerThinking(), env.Profile.LookupThinking(), env.Profile.ActThinking(), env.Profile.BuildThink())
		return 0
	}
	if len(args) == 1 {
		return setOne(env, "agent.think_mode", args, session)
	}
	path := map[string]string{
		"answer": "agent.think_answer",
		"lookup": "agent.think_lookup",
		"act":    "agent.think_act",
		"build":  "agent.build_thinking",
	}[strings.ToLower(args[0])]
	if path == "" {
		fmt.Fprintln(env.errw(), "think is an enum; allowed: on, off, auto, answer, lookup, act, build")
		return 2
	}
	return setOne(env, path, args[1:], session)
}

func setTemperature(env *Env, args []string, session bool) int {
	if len(args) == 0 {
		t := env.Profile.LLM.Temperature
		fmt.Fprintf(env.out(), "answer %g\nlookup %g\nact %g\nbuild %g\n", t.Answer, t.Lookup, t.Act, t.Build)
		return 0
	}
	if len(args) == 1 {
		return setOne(env, "llm.temperature.answer", args, session)
	}
	intent := strings.ToLower(args[0])
	if intent != "answer" && intent != "lookup" && intent != "act" && intent != "build" {
		// temperature 0.2 build  -> value then intent, or intent then value
		if _, err := strconv.ParseFloat(args[0], 64); err == nil {
			intent = strings.ToLower(args[1])
			return setOne(env, "llm.temperature."+intent, []string{args[0]}, session)
		}
		fmt.Fprintln(env.errw(), "temperature is a float; allowed: answer, lookup, act, build")
		return 2
	}
	return setOne(env, "llm.temperature."+intent, args[1:], session)
}

func setPlan(env *Env, args []string, session bool) int {
	if len(args) < 2 {
		fmt.Fprintf(env.out(), "act_low %s\nact_high %s\nbuild %s\n", onOff(env.Profile.Agent.Plan.ActLow), onOff(env.Profile.Agent.Plan.ActHigh), onOff(env.Profile.Agent.Plan.Build))
		return 0
	}
	path := map[string]string{"act_low": "agent.plan.act_low", "act_high": "agent.plan.act_high", "build": "agent.plan.build"}[args[0]]
	if path == "" {
		fmt.Fprintln(env.errw(), "plan is a bool; allowed: act_low, act_high, build")
		return 2
	}
	return setOne(env, path, args[1:], session)
}

func setElide(env *Env, args []string, session bool) int {
	if len(args) == 0 {
		fmt.Fprintf(env.out(), "trigger %g\ntarget %g\n", env.Profile.Context.ElideTrigger, env.Profile.Context.ElideTarget)
		return 0
	}
	if len(args) != 2 {
		fmt.Fprintln(env.errw(), "usage: /set elide <trigger> <target>")
		return 2
	}
	if code := setOne(env, "context.elide_trigger", []string{args[0]}, session); code != 0 {
		return code
	}
	return setOne(env, "context.elide_target", []string{args[1]}, session)
}

func setCtx(env *Env, args []string, session bool) int {
	if len(args) < 2 {
		fmt.Fprintf(env.out(), "warn %d\nhot %d\n", env.Profile.UI.CtxWarnPct, env.Profile.UI.CtxHotPct)
		return 0
	}
	path := map[string]string{"warn": "ui.ctx_warn_pct", "hot": "ui.ctx_hot_pct"}[args[0]]
	if path == "" {
		fmt.Fprintln(env.errw(), "ctx is an int; allowed: warn, hot")
		return 2
	}
	return setOne(env, path, args[1:], session)
}

func setTimeout(env *Env, args []string, session bool) int {
	if len(args) < 2 {
		fmt.Fprint(env.out(), formatCandidates([]config.Key{
			mustKey("tools.shell.default_timeout_sec"),
			mustKey("llm.idle_timeout_seconds"),
		}))
		return 2
	}
	path := map[string]string{"shell": "tools.shell.default_timeout_sec", "model": "llm.idle_timeout_seconds"}[args[0]]
	if path == "" {
		fmt.Fprintln(env.errw(), "timeout is an int; allowed: shell, model")
		return 2
	}
	return setOne(env, path, args[1:], session)
}

func setWeb(env *Env, args []string, session bool) int {
	if len(args) == 0 {
		fmt.Fprintf(env.out(), "web %s\nresults %d\n", onOff(env.Profile.Tools.Web.Enabled), env.Profile.Tools.Web.Results)
		return 0
	}
	if args[0] == "results" {
		return setOne(env, "tools.web.results", args[1:], session)
	}
	return setBoolKey(env, "tools.web.enabled", args, session)
}

func setURLRole(env *Env, path string, args []string, session bool) int {
	if len(args) == 0 {
		return setOne(env, path, nil, session)
	}
	if strings.EqualFold(args[0], "off") || args[0] == "default" {
		k, _ := config.KeyByPath(path)
		return commitKey(env, k, "", session, true)
	}
	return setOne(env, path, args, session)
}

func setDatabase(env *Env, args []string, session bool) int {
	k, _ := config.KeyByPath("database.url")
	if len(args) == 0 {
		fmt.Fprint(env.out(), describeKey(env, k))
		return 0
	}
	if strings.EqualFold(args[0], "off") || args[0] == "default" {
		return commitKey(env, k, "", session, true)
	}
	raw := strings.Join(args, " ")
	if configURLPassword(raw) {
		fmt.Fprintln(env.errw(), "database is a string; a password is not allowed (use ~/.pgpass or KONSOLAI_DATABASE_URL)")
		return 2
	}
	return commitKey(env, k, raw, session, false)
}

func configURLPassword(raw string) bool {
	return strings.Contains(raw, "://") && strings.Contains(raw, "@") && strings.Contains(strings.Split(raw, "://")[1], ":") && strings.Index(raw, ":") < strings.Index(raw, "@")
}

func setModel(ctx context.Context, env *Env, args []string, session bool) int {
	if len(args) == 0 {
		fmt.Fprint(env.out(), formatModelTable(ctx, env))
		return 0
	}
	switch args[0] {
	case "main":
		if len(args) != 2 {
			fmt.Fprintln(env.errw(), "usage: /set model main <name>")
			return 2
		}
		return setRole(env, "roles.main", args[1], session)
	case "fast":
		if len(args) != 2 {
			fmt.Fprintln(env.errw(), "usage: /set model fast <name>|off")
			return 2
		}
		if args[1] == "off" || args[1] == "default" {
			k, _ := config.KeyByPath("roles.fast")
			return commitKey(env, k, "", session, true)
		}
		return setRole(env, "roles.fast", args[1], session)
	case "add":
		if len(args) < 3 {
			fmt.Fprintln(env.errw(), "usage: /set model add <name> <endpoint> [id]")
			return 2
		}
		return addModel(env, args[1], args[2], joinOpt(args, 3), session)
	case "remove":
		if len(args) != 2 {
			fmt.Fprintln(env.errw(), "usage: /set model remove <name>")
			return 2
		}
		return removeModel(env, args[1], session)
	case "discover":
		fmt.Fprint(env.out(), discoverModels(ctx, env.Profile))
		return 0
	default:
		fmt.Fprintln(env.errw(), "usage: /set model [main|fast|add|remove|discover]")
		return 2
	}
}

func joinOpt(args []string, i int) string {
	if i >= len(args) {
		return ""
	}
	return args[i]
}

func setRole(env *Env, path, name string, session bool) int {
	if _, ok := env.Profile.Models[name]; !ok {
		fmt.Fprintf(env.errw(), "config: %s %q is not a configured model; valid names: %s\n", path, name, strings.Join(env.Profile.ModelNames(), ", "))
		return 2
	}
	k, _ := config.KeyByPath(path)
	return commitKey(env, k, name, session, false)
}

func addModel(env *Env, name, endpoint, id string, session bool) int {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(endpoint) == "" {
		fmt.Fprintln(env.errw(), "usage: /set model add <name> <endpoint> [id]")
		return 2
	}
	c := env.Profile
	if c.Models == nil {
		c.Models = map[string]config.Model{}
	}
	m := c.Models[name]
	m.Endpoint = endpoint
	if id != "" {
		m.Model = id
	}
	c.Models[name] = m
	if err := config.Validate(c); err != nil && strings.Contains(err.Error(), name) {
		fmt.Fprintln(env.errw(), err)
		return 2
	}
	env.Profile = c
	if env.Runner != nil {
		env.Runner.Profile = c
		env.Runner.RebindNext = true
	}
	if !session {
		if err := config.UpsertModel(configPath(env), name, m, false); err != nil {
			fmt.Fprintln(env.errw(), err)
			return 1
		}
	}
	fmt.Fprintln(env.out(), "from the next turn")
	return 0
}

func removeModel(env *Env, name string, session bool) int {
	if env.Profile.Roles.Main == name || env.Profile.Roles.Fast == name {
		fmt.Fprintf(env.errw(), "model %s is in use; point the role elsewhere first\n", name)
		return 2
	}
	c := env.Profile
	delete(c.Models, name)
	env.Profile = c
	if env.Runner != nil {
		env.Runner.Profile = c
		env.Runner.RebindNext = true
	}
	if !session {
		if err := config.UpsertModel(configPath(env), name, config.Model{}, true); err != nil {
			fmt.Fprintln(env.errw(), err)
			return 1
		}
	}
	fmt.Fprintln(env.out(), "from the next turn")
	return 0
}

func commitKey(env *Env, k config.Key, raw string, session, remove bool) int {
	if k.Secret && !remove && configURLPassword(raw) {
		fmt.Fprintf(env.errw(), "%s is a secret; a password is not allowed (use ~/.pgpass or the environment)\n", k.Path)
		return 2
	}
	c := env.Profile
	if remove {
		k.ResetDefault(&c)
	} else if err := k.SetString(&c, raw); err != nil {
		fmt.Fprintln(env.errw(), err)
		return 2
	}
	env.Profile = c
	if env.Runner != nil {
		env.Runner.Profile = c
		if k.Apply == config.ApplyNext || k.Apply == config.ApplyRestart {
			env.Runner.RebindNext = true
		}
	}
	if session {
		if env.Session == nil {
			env.Session = map[string]string{}
		}
		if remove {
			env.Session[k.Path] = "default"
		} else {
			env.Session[k.Path] = k.Present(c)
		}
	} else if err := config.SetFileKey(configPath(env), k.Path, k.Present(c), remove); err != nil {
		fmt.Fprintln(env.errw(), err)
		return 1
	}
	applyLive(env, k)
	fmt.Fprintln(env.out(), k.ApplyPhrase())
	if !session {
		if name, ok := k.EnvOverride(); ok {
			fmt.Fprintln(env.out(), ui.Current().Muted("overridden by "+name+" in this shell"))
		}
	}
	return 0
}

func applyLive(env *Env, k config.Key) {
	if env == nil {
		return
	}
	switch k.Path {
	case "ui.theme", "ui.appearance", "ui.code_highlight", "ui.strip_emoji", "ui.cursor_color":
		ui.ApplyUIOpts(env.Profile.PaletteName(), env.Profile.Appearance(), env.Plain, env.Profile.CodeHighlightOn(), env.Profile.StripEmojiOn())
		if env.Profile.CursorColorOn() && theme.Enabled() {
			theme.ApplyCursor(env.out())
		}
	}
}

func configPath(env *Env) string {
	if env != nil && env.ConfigPath != "" {
		return env.ConfigPath
	}
	return config.DefaultPath()
}

func describeKey(env *Env, k config.Key) string {
	c := config.Builtin()
	if env != nil {
		c = env.Profile
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", k.Path)
	fmt.Fprintf(&b, "value    %s\n", k.Present(c))
	fmt.Fprintf(&b, "source   %s\n", keySource(env, k))
	def := config.Builtin()
	fmt.Fprintf(&b, "default  %s\n", k.Present(def))
	if len(k.Allowed) > 0 {
		fmt.Fprintf(&b, "allowed  %s\n", strings.Join(k.Allowed, ", "))
	} else if k.Path == "ui.theme" {
		fmt.Fprintf(&b, "allowed  %s\n", strings.Join(config.ThemeNames, ", "))
	} else {
		fmt.Fprintf(&b, "type     %s\n", k.KindName())
	}
	fmt.Fprintf(&b, "doc      %s\n", k.Doc)
	return b.String()
}

func keySource(env *Env, k config.Key) string {
	if env != nil && env.Session != nil {
		if _, ok := env.Session[k.Path]; ok {
			return "session"
		}
	}
	if _, ok := k.EnvOverride(); ok {
		return "env"
	}
	def := config.Builtin()
	if env != nil && k.Present(env.Profile) != k.Present(def) {
		return "file"
	}
	return "default"
}

func mustKey(path string) config.Key {
	k, _ := config.KeyByPath(path)
	return k
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func formatCandidates(keys []config.Key) string {
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "  %s  %s\n", k.Path, ui.Current().Muted(k.Doc))
	}
	return b.String()
}

func formatSetList(c config.Config) string {
	return formatSetMenu(menuRows(c), -1, false)
}

func formatModelTable(ctx context.Context, env *Env) string {
	c := env.Profile
	names := c.ModelNames()
	var b strings.Builder
	for _, name := range names {
		m := c.Models[name]
		mark := "  "
		label := name
		var roles []string
		if c.Roles.Main == name {
			roles = append(roles, "main")
			mark = "› "
		}
		if c.Roles.Fast == name {
			roles = append(roles, "fast")
			if mark == "  " {
				mark = "› "
			}
		}
		role := strings.Join(roles, ",")
		if role == "" {
			role = "-"
		}
		host := turn.EndpointHost(m.Endpoint)
		id := m.Model
		if id == "" {
			id = "-"
		}
		status := probeStatus(ctx, m.Endpoint, env)
		line := fmt.Sprintf("%s%s  %s  %s  %s  ctx %d  %s", mark, label, role, host, id, m.Context, status)
		if mark == "› " {
			line = ui.Current().Accent(line)
		}
		fmt.Fprintln(&b, line)
	}
	if c.EmbedURL() == "" {
		fmt.Fprintln(&b, "  embed  "+ui.Current().Muted("not configured"))
	} else {
		fmt.Fprintf(&b, "  embed  %s  %s\n", turn.EndpointHost(c.EmbedURL()), probeStatus(ctx, c.EmbedURL(), env))
	}
	if c.RerankURL() == "" {
		fmt.Fprintln(&b, "  rerank  "+ui.Current().Muted("not configured"))
	} else {
		fmt.Fprintf(&b, "  rerank  %s  %s\n", turn.EndpointHost(c.RerankURL()), probeStatus(ctx, c.RerankURL(), env))
	}
	return b.String()
}

func probeStatus(ctx context.Context, endpoint string, env *Env) string {
	if strings.TrimSpace(endpoint) == "" {
		return "down"
	}
	timeout := 300 * time.Millisecond
	if env != nil {
		if d := env.Profile.ProbeTimeout(); d > 0 {
			timeout = d
		}
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/models", nil)
	if err != nil {
		return "down"
	}
	t0 := time.Now()
	resp, err := http.DefaultClient.Do(req)
	lat := time.Since(t0).Round(time.Millisecond)
	if err != nil {
		return "down"
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "down"
	}
	word := "up"
	if env != nil && lat > env.Profile.ModelSlow() {
		word = "slow"
	}
	extra := ""
	if env != nil && env.Mem != nil && env.Mem.Store() != nil {
		if rate, err := env.Mem.Store().ModelRate(ctx, "", env.Profile.ModelRateWindow()); err == nil && rate != nil {
			extra = fmt.Sprintf("  %.0f tok/s", *rate)
		}
	}
	return fmt.Sprintf("%s %s%s", word, lat, extra)
}

func discoverModels(ctx context.Context, c config.Config) string {
	seen := map[string]map[string]bool{}
	var endpoints []string
	add := func(ep string) {
		ep = strings.TrimRight(strings.TrimSpace(ep), "/")
		if ep == "" {
			return
		}
		if _, ok := seen[ep]; ok {
			return
		}
		seen[ep] = map[string]bool{}
		endpoints = append(endpoints, ep)
	}
	for _, name := range c.ModelNames() {
		add(c.Models[name].Endpoint)
	}
	configured := map[string]bool{}
	for _, m := range c.Models {
		if m.Model != "" {
			configured[m.Model] = true
		}
	}
	sort.Strings(endpoints)
	var b strings.Builder
	for _, ep := range endpoints {
		ids := listModelIDs(ctx, ep)
		for _, id := range ids {
			if configured[id] {
				continue
			}
			fmt.Fprintf(&b, "/set model add %s %s %s\n", slugModel(id), ep, id)
		}
	}
	if b.Len() == 0 {
		return "no new models\n"
	}
	return b.String()
}

func listModelIDs(ctx context.Context, endpoint string) []string {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, endpoint+"/models", nil)
	if err != nil {
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}
	var ids []string
	for _, d := range parsed.Data {
		if d.ID != "" {
			ids = append(ids, d.ID)
		}
	}
	return ids
}

func slugModel(id string) string {
	id = strings.ToLower(id)
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			if b.Len() > 0 && b.String()[b.Len()-1] != '-' {
				b.WriteByte('-')
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "model"
	}
	if len(s) > 32 {
		s = s[:32]
	}
	return s
}
