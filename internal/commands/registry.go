package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/SniksaX/Konsolai/internal/agent"
	"github.com/SniksaX/Konsolai/internal/clip"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/embed"
	"github.com/SniksaX/Konsolai/internal/memory"
	"github.com/SniksaX/Konsolai/internal/turn"
	"github.com/SniksaX/Konsolai/internal/ui"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
)

type Arg struct {
	Name     string
	Help     string
	Required bool
}

type Command struct {
	Name     string
	Aliases  []string
	Usage    string
	Summary  string
	Example  string
	Args     []Arg
	Tree     func(*Env) []Choice
	ReplOnly bool
	Run      func(ctx context.Context, env *Env, args []string) int
}

func (c Command) HasRequiredArgs() bool {
	for _, a := range c.Args {
		if a.Required {
			return true
		}
	}
	return false
}

func (c Command) TakesArgs() bool {
	return len(c.Args) > 0
}

type Env struct {
	Runner       *turn.Runner
	Mem          *memory.Guard
	Profile      config.Profile
	Copy         func(ctx context.Context, text string) (string, error)
	Out          io.Writer
	Err          io.Writer
	Cwd          string
	Yes          bool
	Version      string
	Plain        bool
	Confirm      agent.Confirmer
	Undo         func(ctx context.Context) int
	Remember     func(ctx context.Context, args []string) int
	Facts        func(ctx context.Context) int
	Forget       func(ctx context.Context, args []string) int
	Prune        func(ctx context.Context) int
	LoadHistory  func(ctx context.Context, limit int) []histItem
	RunTurn      func(ctx context.Context, req turn.Request) turn.Result
	CloseAll     func(ctx context.Context) error
	InRepl       bool
	OnCwd        func(dir string) error
	OnYes        func(on bool)
	OnTrace      func(on bool)
	OnNewSession func()
	OnCompact    func(ctx context.Context) int
	OnExit       func() int
	OnCmd        func() int
	OnCmdClear   func()
	OnClear      func()
	FailedShell  func() (cmd string, exit int, output string, ok bool)
	Embed        *embed.Client
	ProjectID    *int64
	ConfigPath   string
	Session      map[string]string
}

func (e *Env) out() io.Writer {
	if e != nil && e.Out != nil {
		return e.Out
	}
	return os.Stdout
}

func (e *Env) errw() io.Writer {
	if e != nil && e.Err != nil {
		return e.Err
	}
	return os.Stderr
}

func (e *Env) copy(ctx context.Context, text string) (string, error) {
	if e != nil && e.Copy != nil {
		return e.Copy(ctx, text)
	}
	return clip.Copy(ctx, text)
}

type Registry struct {
	cmds []Command
}

func New(cmds ...Command) *Registry {
	return &Registry{cmds: cmds}
}

func (r *Registry) SlashLines() string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range r.cmds {
		fmt.Fprintf(&b, "/%s: %s\n", c.Name, c.Summary)
	}
	return b.String()
}

func (r *Registry) All() []Command { return r.cmds }

func (r *Registry) Lookup(name string) *Command {
	name = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "/")
	for i := range r.cmds {
		c := &r.cmds[i]
		if c.Name == name {
			return c
		}
		for _, a := range c.Aliases {
			if a == name {
				return c
			}
		}
	}
	return nil
}

func (r *Registry) Dispatch(ctx context.Context, env *Env, name string, args []string) int {
	c := r.Lookup(name)
	if c == nil {
		msg := "unknown command /" + strings.TrimPrefix(name, "/") + "; see /help"
		if sug := r.suggest(name); sug != "" {
			msg += "\ndid you mean /" + sug + "?"
		}
		fmt.Fprintln(env.errw(), msg)
		return 2
	}
	if c.ReplOnly && (env == nil || !env.InRepl) {
		fmt.Fprintln(env.errw(), "/"+c.Name+" is only available in the REPL")
		return 2
	}
	return c.Run(ctx, env, args)
}

func (r *Registry) suggest(name string) string {
	name = strings.TrimPrefix(strings.ToLower(name), "/")
	best := ""
	bestD := 3
	for _, c := range r.cmds {
		d := editDist(name, c.Name)
		if d < bestD {
			bestD = d
			best = c.Name
		}
		for _, a := range c.Aliases {
			d := editDist(name, a)
			if d < bestD {
				bestD = d
				best = c.Name
			}
		}
	}
	if bestD <= 2 {
		return best
	}
	return ""
}

func editDist(a, b string) int {
	if a == b {
		return 0
	}
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 0
			if ra[i-1] != rb[j-1] {
				cost = 1
			}
			del := prev[j] + 1
			ins := cur[j-1] + 1
			sub := prev[j-1] + cost
			cur[j] = del
			if ins < cur[j] {
				cur[j] = ins
			}
			if sub < cur[j] {
				cur[j] = sub
			}
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func commandNames(c Command) string {
	s := "/" + c.Name
	for _, a := range c.Aliases {
		s += ", /" + a
	}
	return s
}

func (c Command) DisplayNames() string { return commandNames(c) }

func SlashRow(names, desc string, selected bool) string {
	p := ui.Current()
	if selected {
		return p.Accent("› ") + p.Heading(names) + "  " + p.Meta(desc)
	}
	return "  " + theme.Style(theme.Primary, names) + "  " + p.Muted(desc)
}

func runHelp(ctx context.Context, env *Env, _ []string) int {
	for _, c := range Default().All() {
		fmt.Fprintln(env.out(), SlashRow(commandNames(c), c.Summary, false))
	}
	return 0
}

func Default() *Registry {
	return New(
		Command{Name: "help", Usage: "help", Summary: "Show this list of commands", Example: "/help", Run: runHelp},
		Command{Name: "copy", Usage: "copy answer|all|code|cmd [n]", Summary: "Copy the last answer, pair, code block or command",
			Args: []Arg{{Name: "what", Help: "answer, all, code or cmd", Required: true}, {Name: "n", Help: "how many (max 5)"}},
			Tree: copyTree, Example: "/copy code", Run: runCopy},
		Command{Name: "history", Usage: "history [n]", Summary: "List recent sessions",
			Args: []Arg{{Name: "n", Help: "how many sessions"}}, Tree: nTree("<n>  sessions, default 10, max 50", 1, 50), Example: "/history 5", Run: runHistory},
		Command{Name: "show", Usage: "show <n>", Summary: "Reprint a session's prompts and answers",
			Args: []Arg{{Name: "n", Help: "session index", Required: true}}, Tree: nTree("<n>", 1, 50), Example: "/show 1", Run: runShow},
		Command{Name: "resume", Usage: "resume <n> \"<request>\"", Summary: "Continue session n with a new request",
			Args: []Arg{{Name: "n", Help: "session index", Required: true}, {Name: "request", Help: "the next prompt", Required: true}},
			Tree: resumeTree, Example: "/resume 1 \"continue\"", Run: runResume},
		Command{Name: "new", Usage: "new", Summary: "Close resumable sessions so the next request starts fresh", Example: "/new", Run: runNew},
		Command{Name: "exit", Aliases: []string{"quit"}, Usage: "exit", Summary: "Leave the REPL", Example: "/exit", ReplOnly: true, Run: runExit},
		Command{Name: "clear", Usage: "clear", Summary: "Clear the screen", Example: "/clear", ReplOnly: true, Run: runClear},
		Command{Name: "cd", Usage: "cd <dir>", Summary: "Change the working directory",
			Args: []Arg{{Name: "dir", Help: "directory to enter", Required: true}}, Tree: cdTree, Example: "/cd ..", ReplOnly: true, Run: runCD},
		Command{Name: "cmd", Usage: "cmd [clear]", Summary: "Toggle shell mode",
			Args: []Arg{{Name: "clear", Help: "clear the shell history"}}, Tree: cmdTree, Example: "/cmd", ReplOnly: true, Run: runCmd},
		Command{Name: "yes", Usage: "yes on|off", Summary: "Auto-approve for this REPL session",
			Args: []Arg{{Name: "mode", Help: "on or off", Required: true}}, Tree: onOffTree("Auto-approve for this session", "Ask before mutating actions"), Example: "/yes on", ReplOnly: true, Run: runYes},
		Command{Name: "trace", Usage: "trace on|off", Summary: "Toggle per-call traces",
			Args: []Arg{{Name: "mode", Help: "on or off", Required: true}}, Tree: onOffTree("Print per-call traces", "Hide per-call traces"), Example: "/trace on", ReplOnly: true, Run: runTrace},
		Command{Name: "think", Usage: "think on|off|auto", Summary: "Override thinking for this session",
			Args: []Arg{{Name: "mode", Help: "on, off or auto", Required: true}}, Tree: thinkCmdTree, Example: "/think auto", ReplOnly: true, Run: runThink},
		Command{Name: "model", Usage: "model", Summary: "Show model, window and endpoint", Example: "/model", ReplOnly: true, Run: runModel},
		Command{Name: "compact", Usage: "compact", Summary: "Summarize earlier messages in this session", Example: "/compact", ReplOnly: true, Run: runCompact},
		Command{Name: "undo", Usage: "undo", Summary: "Restore files changed in recent sessions", Example: "/undo", Run: runUndo},
		Command{Name: "diff", Usage: "diff [n]", Summary: "Show file diffs from a session",
			Args: []Arg{{Name: "n", Help: "session index"}}, Tree: nTree("<n>  session, default 1, max 50", 1, 50), Example: "/diff", Run: runDiff},
		Command{Name: "run", Usage: "run [k]", Summary: "Run a bash/sh block from the last answer",
			Args: []Arg{{Name: "k", Help: "block index"}}, Tree: nTree("<k>  block index", 1, 50), Example: "/run", Run: runRun},
		Command{Name: "output", Usage: "output [n]", Summary: "Reprint the full output of a recent command",
			Args: []Arg{{Name: "n", Help: "how many lines"}}, Tree: nTree("<n>  how many, max 5", 1, 5), Example: "/output", Run: runOutput},
		Command{Name: "status", Usage: "status", Summary: "Show version, model, database and environment", Example: "/status", Run: runStatus},
		Command{Name: "config", Usage: "config path|show|validate|diff|init [--force|--from-current]", Summary: "Show or write the YAML config",
			Args: []Arg{{Name: "action", Help: "path, show, validate, diff or init", Required: true}}, Tree: configTree, Example: "/config validate", Run: runConfig},
		Command{Name: "set", Usage: "set [name] [value] [--session]", Summary: "Show or change a setting",
			Args: []Arg{{Name: "name", Help: "setting name or path"}, {Name: "value", Help: "value, or default"}}, Tree: setTree, Example: "/set theme glacier", Run: runSet},
		Command{Name: "models", Usage: "models", Summary: "Show configured models", Example: "/models", Run: runModels},
		Command{Name: "theme", Usage: "theme [name]", Summary: "List palettes or apply one",
			Args: []Arg{{Name: "name", Help: "theme name"}}, Tree: themeCmdTree, Example: "/theme glacier", Run: runTheme},
		Command{Name: "index", Usage: "index [path]|status|clear", Summary: "Index the project for recall",
			Args: []Arg{{Name: "action", Help: "path, status or clear"}}, Tree: indexTree, Example: "/index status", Run: runIndex},
		Command{Name: "recall", Usage: "recall \"<query>\" [--scope X]", Summary: "Search indexed memory without calling the model",
			Args: []Arg{{Name: "query", Help: "search text", Required: true}}, Tree: recallTree, Example: "/recall \"auth\"", Run: runRecall},
		Command{Name: "remember", Usage: "remember [--project] \"<fact>\"", Summary: "Store a fact",
			Args: []Arg{{Name: "fact", Help: "the fact to store", Required: true}}, Tree: rememberTree, Example: "/remember \"use gofmt\"", Run: runRemember},
		Command{Name: "facts", Usage: "facts", Summary: "List remembered facts", Example: "/facts", Run: runFacts},
		Command{Name: "forget", Usage: "forget user|project <id>", Summary: "Delete a fact",
			Args: []Arg{{Name: "scope", Help: "user or project", Required: true}, {Name: "id", Help: "fact id", Required: true}},
			Tree: forgetTree, Example: "/forget user 3", Run: runForget},
		Command{Name: "prune", Usage: "prune", Summary: "Prune old sessions now", Example: "/prune", Run: runPrune},
		Command{Name: "stats", Usage: "stats [today|week|all]", Summary: "Show calls, tokens, and latency",
			Args: []Arg{{Name: "window", Help: "today, week, or all"}}, Tree: statsTree, Example: "/stats week", Run: runStats},
		Command{Name: "doctor", Usage: "doctor", Summary: "Check config, endpoints, database, and local tools", Example: "/doctor", Run: runDoctor},
		Command{Name: "save", Usage: "save [file] [--force]", Summary: "Write the last answer to a Markdown file",
			Args: []Arg{{Name: "file", Help: "output path"}}, Tree: fileTree, Example: "/save", Run: runSave},
		Command{Name: "pdf", Usage: "pdf [file] [--force]", Summary: "Render the last answer to a PDF",
			Args: []Arg{{Name: "file", Help: "output path"}}, Tree: fileTree, Example: "/pdf", Run: runPDF},
		Command{Name: "explain", Usage: "explain", Summary: "Explain the last failed shell command (set PROMPT_COMMAND=\"history -a\")",
			Example: "/explain", Run: runExplain},
	)
}

func NoteUseSlash(w io.Writer, name string) {
	fmt.Fprintln(w, ui.Current().Muted("note: use /"+name))
}
