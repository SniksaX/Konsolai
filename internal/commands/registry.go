package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/siraj/konsolai/internal/agent"
	"github.com/siraj/konsolai/internal/clip"
	"github.com/siraj/konsolai/internal/config"
	"github.com/siraj/konsolai/internal/memory"
	"github.com/siraj/konsolai/internal/turn"
	"github.com/siraj/konsolai/internal/ui"
)

type Command struct {
	Name     string
	Aliases  []string
	Usage    string
	Summary  string
	ReplOnly bool
	Run      func(ctx context.Context, env *Env, args []string) int
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

func runHelp(ctx context.Context, env *Env, _ []string) int {
	cmds := Default().All()
	width := 0
	for _, c := range cmds {
		u := "/" + c.Usage
		if len(u) > width {
			width = len(u)
		}
	}
	for _, c := range cmds {
		fmt.Fprintf(env.out(), "%-*s  %s\n", width, "/"+c.Usage, c.Summary)
	}
	return 0
}

func Default() *Registry {
	return New(
		Command{Name: "help", Usage: "help", Summary: "Show this list of commands", Run: runHelp},
		Command{Name: "copy", Usage: "copy answer|all|code|cmd [n]", Summary: "Copy the last answer, pair, code block or command", Run: runCopy},
		Command{Name: "history", Usage: "history [n]", Summary: "List recent sessions", Run: runHistory},
		Command{Name: "show", Usage: "show <n>", Summary: "Reprint a session's prompts and answers", Run: runShow},
		Command{Name: "resume", Usage: "resume <n> \"<request>\"", Summary: "Continue session n with a new request", Run: runResume},
		Command{Name: "new", Usage: "new", Summary: "Close resumable sessions so the next request starts fresh", Run: runNew},
		Command{Name: "exit", Usage: "exit", Summary: "Leave the REPL", ReplOnly: true, Run: runExit},
		Command{Name: "quit", Usage: "quit", Summary: "Leave the REPL", ReplOnly: true, Run: runExit},
		Command{Name: "clear", Usage: "clear", Summary: "Clear the screen", ReplOnly: true, Run: runClear},
		Command{Name: "cd", Usage: "cd <dir>", Summary: "Change the working directory", ReplOnly: true, Run: runCD},
		Command{Name: "cmd", Usage: "cmd [clear]", Summary: "Toggle shell mode", ReplOnly: true, Run: runCmd},
		Command{Name: "yes", Usage: "yes on|off", Summary: "Auto-approve for this REPL session", ReplOnly: true, Run: runYes},
		Command{Name: "trace", Usage: "trace on|off", Summary: "Toggle per-call traces", ReplOnly: true, Run: runTrace},
		Command{Name: "think", Usage: "think on|off|auto", Summary: "Override thinking for this session", ReplOnly: true, Run: runThink},
		Command{Name: "model", Usage: "model", Summary: "Show model, window and endpoint", ReplOnly: true, Run: runModel},
		Command{Name: "compact", Usage: "compact", Summary: "Summarize earlier messages in this session", ReplOnly: true, Run: runCompact},
		Command{Name: "undo", Usage: "undo", Summary: "Restore files changed in recent sessions", Run: runUndo},
		Command{Name: "diff", Usage: "diff [n]", Summary: "Show file diffs from a session", Run: runDiff},
		Command{Name: "run", Usage: "run [k]", Summary: "Run a bash/sh block from the last answer", Run: runRun},
		Command{Name: "output", Usage: "output [n]", Summary: "Reprint the full output of a recent command", Run: runOutput},
		Command{Name: "status", Usage: "status", Summary: "Show version, model, database and environment", Run: runStatus},
		Command{Name: "remember", Usage: "remember [--project] \"<fact>\"", Summary: "Store a fact", Run: runRemember},
		Command{Name: "facts", Usage: "facts", Summary: "List remembered facts", Run: runFacts},
		Command{Name: "forget", Usage: "forget user|project <id>", Summary: "Delete a fact", Run: runForget},
		Command{Name: "prune", Usage: "prune", Summary: "Prune old sessions now", Run: runPrune},
	)
}

func NoteUseSlash(w io.Writer, name string) {
	fmt.Fprintln(w, ui.Current().Muted("note: use /"+name))
}
