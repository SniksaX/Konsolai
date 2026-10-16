package turn

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/siraj/konsolai/internal/agent"
	"github.com/siraj/konsolai/internal/config"
	"github.com/siraj/konsolai/internal/intent"
	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/memory"
	"github.com/siraj/konsolai/internal/ui"
)

type Runner struct {
	Profile     config.Profile
	Client      *llm.Client
	Mem         *memory.Guard
	Out         io.Writer
	Err         io.Writer
	OpenConfirm func(yes bool) (agent.Confirmer, func(), error)
	Copy        func(ctx context.Context, text string) (backend string, err error)
	Version     string
}

type Request struct {
	Text          string
	Stdin         string
	Intent        string
	Yes           bool
	Trace         bool
	Plain         bool
	New           bool
	ResumeSession string
	Cwd           string
}

type Result struct {
	SessionID string
	Intent    string
	ExitCode  int
	Answer    string
}

type ResumeCandidate struct {
	ID       string
	Title    string
	LastUser string
	Age      time.Duration
	Messages []llm.Message
}

func (r *Runner) out() io.Writer {
	if r.Out != nil {
		return r.Out
	}
	return os.Stdout
}

func (r *Runner) errw() io.Writer {
	if r.Err != nil {
		return r.Err
	}
	return os.Stderr
}

func (r *Runner) client() *llm.Client {
	if r.Client != nil {
		return r.Client
	}
	c := llm.New(r.Profile.Endpoint, r.Profile.Model)
	c.SetIdleTimeout(time.Duration(r.Profile.LLMIdleTimeoutSeconds) * time.Second)
	r.Client = c
	return c
}

func (r *Runner) mem() *memory.Guard {
	if r.Mem != nil {
		return r.Mem
	}
	return memory.NewGuard(nil, nil)
}

func (r *Runner) confirm(yes bool) (agent.Confirmer, func(), error) {
	if r.OpenConfirm != nil {
		return r.OpenConfirm(yes)
	}
	p, err := ui.OpenPrompter(yes)
	if err != nil {
		return nil, func() {}, err
	}
	return p, func() { _ = p.Close() }, nil
}

func ThinkingFor(dec intent.Decision, buildThinking string) agent.ThinkingPolicy {
	if dec.Intent == intent.Build {
		return agent.ParseBuildThinking(buildThinking)
	}
	if dec.Reasoning {
		return agent.ThinkOn
	}
	return agent.ThinkOff
}
