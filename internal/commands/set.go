package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/turn"
	"github.com/SniksaX/Konsolai/internal/ui"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
	"github.com/SniksaX/Konsolai/internal/welcome"
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
		if len(rest) >= 2 && rest[1] == "reset-dim" {
			if session {
				fmt.Fprintln(env.errw(), "reset-dim changes the database")
				return 2
			}
			return resetRAGDim(ctx, env, rest[2:])
		}
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
	case "default":
		return setDefault(ctx, env, rest[1:], session)
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

func setDefault(ctx context.Context, env *Env, args []string, session bool) int {
	yes := env != nil && env.Yes
	kept := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--yes" {
			yes = true
			continue
		}
		kept = append(kept, a)
	}
	args = kept
	if len(args) == 0 || args[0] == "default" {
		fmt.Fprintln(env.errw(), "usage: /set default <name|all>")
		return 2
	}
	if strings.EqualFold(args[0], "all") {
		if len(args) != 1 {
			fmt.Fprintln(env.errw(), "usage: /set default all [--yes]")
			return 2
		}
		return resetAllDefaults(env, session, yes)
	}
	forwarded := []string{args[0], "default"}
	forwarded = append(forwarded, args[1:]...)
	if session {
		forwarded = append(forwarded, "--session")
	}
	return runSet(ctx, env, forwarded)
}

func resetAllDefaults(env *Env, session, yes bool) int {
	if env == nil {
		return 1
	}
	body := config.InitYAML()
	if !session && configURLPassword(body) {
		fmt.Fprintln(env.errw(), "database is a string; a password is not allowed (use ~/.pgpass or KONSOLAI_DATABASE_URL)")
		return 2
	}
	if !yes {
		ok, err := confirmReset(env, factoryDiffSummary(env.Profile))
		if err != nil {
			fmt.Fprintln(env.errw(), err)
			return 1
		}
		if !ok {
			fmt.Fprintln(env.out(), "left unchanged")
			return 1
		}
	}
	if !session {
		backup, err := backupActiveConfig(configPath(env))
		if err != nil {
			fmt.Fprintln(env.errw(), err)
			return 1
		}
		if backup != "" {
			fmt.Fprintln(env.out(), backup)
		}
		if err := config.WriteAtomic(configPath(env), []byte(body)); err != nil {
			fmt.Fprintln(env.errw(), err)
			return 1
		}
	}
	c := config.Builtin()
	env.Profile = c
	env.Session = nil
	if env.Runner != nil {
		env.Runner.Profile = c
		env.Runner.RebindNext = true
	}
	k, _ := config.KeyByPath("ui.theme")
	applyLive(env, k)
	fmt.Fprintln(env.out(), "applied now")
	return 0
}

func factoryDiffSummary(c config.Config) string {
	base := config.Builtin()
	n := 0
	for _, k := range config.Keys() {
		if k.Present(c) != k.Present(base) {
			n++
		}
	}
	return fmt.Sprintf("%d keys differ from the factory values. Models, roles, and endpoints will be reset.", n)
}

func confirmReset(env *Env, summary string) (bool, error) {
	conf := env.Confirm
	if conf == nil {
		p, err := ui.OpenPrompter(false)
		if err != nil {
			return false, fmt.Errorf("commands: confirm: %w", err)
		}
		defer p.Close()
		conf = p
	}
	d, _, err := conf.Confirm(summary, "")
	if err != nil {
		return false, fmt.Errorf("commands: confirm: %w", err)
	}
	return d == ui.Approve || d == ui.ApproveAll, nil
}

const backupKeep = 5

// backupActiveConfig copies the real file behind path to
// <name>.bak-<YYYYmmdd-HHMMSS> beside that file, mode 0600.
// A missing file returns an empty path. Older backups in that directory
// beyond the newest five are removed. Backups elsewhere are left alone.
func backupActiveConfig(path string) (string, error) {
	real, err := config.RealPath(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(real)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("commands: read config: %w", err)
	}
	dir := filepath.Dir(real)
	base := filepath.Base(real)
	var dest string
	var f *os.File
	for try := 0; try < 5; try++ {
		stamp := time.Now().Format("20060102-150405")
		dest = filepath.Join(dir, base+".bak-"+stamp)
		f, err = os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			break
		}
		if !os.IsExist(err) {
			return "", fmt.Errorf("commands: backup config: %w", err)
		}
		time.Sleep(time.Second)
	}
	if f == nil {
		return "", fmt.Errorf("commands: backup config: %s exists", dest)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(dest)
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("commands: backup config: %w", err)
	}
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("commands: backup config: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("commands: backup config: %w", err)
	}
	if err := rotateBackups(dir, base, dest, backupKeep); err != nil {
		return "", err
	}
	ok = true
	return dest, nil
}

func rotateBackups(dir, base, keepPath string, keep int) error {
	matches, err := filepath.Glob(filepath.Join(dir, base+".bak-*"))
	if err != nil {
		return fmt.Errorf("commands: list backups: %w", err)
	}
	var rest []string
	for _, m := range matches {
		if m == keepPath {
			continue
		}
		rest = append(rest, m)
	}
	sort.Strings(rest)
	drop := len(rest) - (keep - 1)
	if drop < 1 {
		return nil
	}
	for _, p := range rest[:drop] {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("commands: remove backup: %w", err)
		}
	}
	return nil
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
	timeout := c.ProbeTimeout()
	slow := c.ModelSlow()
	var b strings.Builder
	p := ui.Current()
	for _, name := range names {
		m := c.Models[name]
		mark := "  "
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
		status, body := welcome.Probe(ctx, m.Endpoint, "main", "models", timeout, slow)
		env.noteServer(m.Endpoint, body)
		id, idServer, nctx, ctxServer := modelIdentity(env, m)
		host := turn.EndpointHost(m.Endpoint)
		head := fmt.Sprintf("%s%s  %s  %s  ", mark, name, role, host)
		if mark == "› " {
			head = p.Accent(head)
		}
		fmt.Fprintf(&b, "%s%s  %s  %s\n", head, paintServer(id, idServer), paintServer(fmt.Sprintf("ctx %d", nctx), ctxServer), status)
	}
	if c.EmbedURL() == "" {
		fmt.Fprintln(&b, "  embed  "+p.Muted("not configured"))
	} else {
		status, _ := welcome.Probe(ctx, c.EmbedURL(), "embed", "embed", timeout, slow)
		fmt.Fprintf(&b, "  embed  %s  %s\n", turn.EndpointHost(c.EmbedURL()), status)
	}
	if c.RerankURL() == "" {
		fmt.Fprintln(&b, "  rerank  "+p.Muted("not configured"))
	} else {
		status, _ := welcome.Probe(ctx, c.RerankURL(), "rerank", "rerank", timeout, slow)
		fmt.Fprintf(&b, "  rerank  %s  %s\n", turn.EndpointHost(c.RerankURL()), status)
	}
	return b.String()
}

func paintServer(text string, fromServer bool) string {
	if fromServer {
		return ui.Current().Muted(text + " (server)")
	}
	return text
}

func modelIdentity(env *Env, m config.Model) (id string, idServer bool, nctx int, ctxServer bool) {
	id = strings.TrimSpace(m.Model)
	nctx = m.Context
	rep, ok := env.serverModel(m.Endpoint)
	if id == "" && ok && rep.ID != "" {
		id = rep.ID
		idServer = true
	}
	if nctx == 0 && ok && rep.Context > 0 {
		nctx = rep.Context
		ctxServer = true
	}
	if id == "" {
		id = "-"
	}
	return id, idServer, nctx, ctxServer
}

type serverSeen struct {
	mu sync.Mutex
	by map[string]seenModel
}

type seenModel struct {
	ID      string
	Context int
}

func (e *Env) noteServer(endpoint string, body []byte) {
	if e == nil || len(body) == 0 {
		return
	}
	rep, ok := parseReported(body, "")
	if !ok {
		return
	}
	e.rememberServer(endpoint, rep)
}

func (e *Env) rememberServer(endpoint string, rep seenModel) {
	if e == nil {
		return
	}
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return
	}
	if e.seen == nil {
		e.seen = &serverSeen{}
	}
	e.seen.mu.Lock()
	defer e.seen.mu.Unlock()
	if e.seen.by == nil {
		e.seen.by = map[string]seenModel{}
	}
	e.seen.by[endpoint] = rep
}

func (e *Env) serverModel(endpoint string) (seenModel, bool) {
	if e == nil || e.seen == nil {
		return seenModel{}, false
	}
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	e.seen.mu.Lock()
	defer e.seen.mu.Unlock()
	rep, ok := e.seen.by[endpoint]
	return rep, ok
}

func parseReported(body []byte, wantID string) (seenModel, bool) {
	items := parseModelItems(body)
	if len(items) == 0 {
		return seenModel{}, false
	}
	pick := items[0]
	if wantID != "" {
		for _, it := range items {
			if it.ID == wantID {
				pick = it
				break
			}
		}
	}
	if pick.ID == "" && pick.Context == 0 {
		return seenModel{}, false
	}
	return seenModel{ID: pick.ID, Context: pick.Context}, true
}

type modelItem struct {
	ID      string
	Context int
}

func parseModelItems(body []byte) []modelItem {
	var parsed struct {
		Data []struct {
			ID            string `json:"id"`
			MaxModelLen   int    `json:"max_model_len"`
			NCtx          int    `json:"n_ctx"`
			ContextLength int    `json:"context_length"`
		} `json:"data"`
		ID            string `json:"id"`
		MaxModelLen   int    `json:"max_model_len"`
		NCtx          int    `json:"n_ctx"`
		ContextLength int    `json:"context_length"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}
	ctxOf := func(maxLen, nCtx, length int) int {
		if maxLen > 0 {
			return maxLen
		}
		if nCtx > 0 {
			return nCtx
		}
		return length
	}
	var items []modelItem
	for _, d := range parsed.Data {
		if d.ID == "" && ctxOf(d.MaxModelLen, d.NCtx, d.ContextLength) == 0 {
			continue
		}
		items = append(items, modelItem{ID: d.ID, Context: ctxOf(d.MaxModelLen, d.NCtx, d.ContextLength)})
	}
	if len(items) == 0 && (parsed.ID != "" || ctxOf(parsed.MaxModelLen, parsed.NCtx, parsed.ContextLength) > 0) {
		items = append(items, modelItem{ID: parsed.ID, Context: ctxOf(parsed.MaxModelLen, parsed.NCtx, parsed.ContextLength)})
	}
	return items
}

func discoverModels(ctx context.Context, c config.Config) string {
	seen := map[string]bool{}
	var endpoints []string
	add := func(ep string) {
		ep = strings.TrimRight(strings.TrimSpace(ep), "/")
		if ep == "" || seen[ep] {
			return
		}
		seen[ep] = true
		endpoints = append(endpoints, ep)
	}
	for _, name := range c.ModelNames() {
		add(c.Models[name].Endpoint)
	}
	sort.Strings(endpoints)
	timeout := c.ProbeTimeout()
	if timeout <= 0 {
		timeout = config.Builtin().ProbeTimeout()
	}
	var b strings.Builder
	for _, ep := range endpoints {
		_, body := welcome.Probe(ctx, ep, "main", "models", timeout, c.ModelSlow())
		for _, item := range parseModelItems(body) {
			if item.ID == "" {
				continue
			}
			if name, ok := configuredAs(c, ep, item.ID); ok {
				fmt.Fprintf(&b, "already configured as %s\n", name)
				continue
			}
			fmt.Fprintf(&b, "/set model add %s %s %s\n", SuggestModelName(item.ID), ep, item.ID)
		}
	}
	if b.Len() == 0 {
		return "no new models\n"
	}
	return b.String()
}

func configuredAs(c config.Config, endpoint, id string) (string, bool) {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	for _, name := range c.ModelNames() {
		m := c.Models[name]
		if strings.TrimRight(strings.TrimSpace(m.Endpoint), "/") == endpoint && m.Model == id {
			return name, true
		}
	}
	return "", false
}
