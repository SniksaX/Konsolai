package turn

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/siraj/konsolai/internal/agent"
	"github.com/siraj/konsolai/internal/config"
	"github.com/siraj/konsolai/internal/intent"
	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/memory"
	"github.com/siraj/konsolai/internal/repo"
	"github.com/siraj/konsolai/internal/store"
	"github.com/siraj/konsolai/internal/tools"
)

func UpsertProject(ctx context.Context, mem *memory.Guard, cwd string) *int64 {
	root := repo.Root(cwd)
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return nil
	}
	s := mem.Store()
	if s == nil {
		return nil
	}
	p := repo.Detect(root)
	id, err := s.UpsertProject(ctx, root, p.Kinds, p.Commands)
	if err != nil {
		mem.Fail(err)
		return nil
	}
	return &id
}

func FindCandidates(ctx context.Context, mem *memory.Guard, prof config.Profile, projectID *int64, cwd string) []ResumeCandidate {
	idle := time.Duration(prof.Memory.SessionIdleMinutes) * time.Minute
	if mem.Store() == nil {
		if sess, ok := memory.LoadLast(prof.Name, cwd, idle); ok {
			age := time.Duration(0)
			if !sess.UpdatedAt.IsZero() {
				age = time.Since(sess.UpdatedAt)
			}
			return []ResumeCandidate{{
				Title: sess.Title, LastUser: lastUserOf(sess.Messages), Age: age, Messages: sess.Messages,
			}}
		}
		return nil
	}
	rows, err := mem.Store().RecentSessions(ctx, prof.Name, projectID, cwd, idle, 3)
	if err != nil {
		mem.Fail(err)
		return nil
	}
	out := make([]ResumeCandidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, ResumeCandidate{ID: row.ID, Title: row.Title, LastUser: row.LastUserMessage, Age: row.Age})
	}
	return out
}

func lastUserOf(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleUser {
			s := msgs[i].Content
			if utf8.RuneCountInString(s) > 120 {
				s = string([]rune(s)[:120])
			}
			return s
		}
	}
	return ""
}

func ToIntentCandidates(cands []ResumeCandidate) []intent.Candidate {
	out := make([]intent.Candidate, len(cands))
	for i, c := range cands {
		out[i] = intent.Candidate{Title: c.Title, LastUser: c.LastUser, Age: c.Age}
	}
	return out
}

func ApplyResume(newSession bool, dec intent.Decision, cands []ResumeCandidate) ResumeCandidate {
	if newSession || dec.ContinuesSession <= 0 || dec.ContinuesSession > len(cands) {
		return ResumeCandidate{}
	}
	return cands[dec.ContinuesSession-1]
}

func CreateSession(ctx context.Context, mem *memory.Guard, prof config.Profile, projectID *int64, cwd, mode, request string) string {
	s := mem.Store()
	if s == nil {
		return ""
	}
	title := request
	if utf8.RuneCountInString(title) > 80 {
		title = string([]rune(title)[:80])
	}
	id, err := s.CreateSession(ctx, store.NewSession{
		Profile: prof.Name, ProjectID: projectID, CWD: cwd,
		Mode: mode, Model: prof.LLM.Model, Title: title,
	})
	if err != nil {
		mem.Fail(err)
		return ""
	}
	return id
}

func LoadFactsBlock(ctx context.Context, mem *memory.Guard, profile string, projectID *int64) string {
	s := mem.Store()
	if s == nil {
		return ""
	}
	user, proj, err := s.ListFacts(ctx, profile, projectID)
	if err != nil {
		mem.Fail(err)
		return ""
	}
	return FormatFacts(user, proj)
}

func FormatFacts(user, proj []store.Fact) string {
	var b string
	write := func(title string, facts []store.Fact) {
		if len(facts) == 0 {
			return
		}
		if len(facts) > 40 {
			facts = facts[:40]
		}
		b += "\n\n" + title + "\n"
		n := 0
		for _, f := range facts {
			line := fmt.Sprintf("- [%d] %s\n", f.ID, f.Fact)
			if n+len(line) > 2048 {
				break
			}
			b += line
			n += len(line)
		}
	}
	write("User facts:", user)
	write("Project facts:", proj)
	return b
}

func SessionStatus(code int) string {
	switch code {
	case 0:
		return "finished"
	case 3, 130:
		return "aborted"
	default:
		return "failed"
	}
}

func DecideIntent(ctx context.Context, client *llm.Client, forceIntent, request string, newSession bool, cands []intent.Candidate, inRepo bool) intent.Decision {
	return DecideIntentFloor(ctx, client, forceIntent, request, newSession, cands, inRepo, nil, nil)
}

func DecideIntentFloor(ctx context.Context, client *llm.Client, forceIntent, request string, newSession bool, cands []intent.Candidate, inRepo bool, verbs, targets []string) intent.Decision {
	floor, hasFloor := intent.ActionFloor(request, verbs, targets)
	if forceIntent != "" {
		n := 0
		if !newSession && len(cands) > 0 {
			n = 1
		}
		return intent.Decision{
			Intent: intent.Intent(forceIntent), Language: "en", Reason: "flag",
			ContinuesSession: n, Effort: intent.EffortLow,
		}
	}
	d, _ := intent.Classify(ctx, client, request, cands, inRepo)
	if hasFloor {
		d = intent.RaiseTo(d, floor)
	}
	return d
}

type sessionTap struct {
	inner  agent.Recorder
	msgs   []llm.Message
	answer string
}

func (t *sessionTap) Message(ctx context.Context, m llm.Message) error {
	if m.Role != llm.RoleSystem {
		t.msgs = append(t.msgs, m)
		if len(t.msgs) > 20 {
			t.msgs = t.msgs[len(t.msgs)-20:]
		}
		if m.Role == llm.RoleAssistant && m.Content != "" && len(m.ToolCalls) == 0 {
			t.answer = m.Content
		}
	}
	if t.inner != nil {
		return t.inner.Message(ctx, m)
	}
	return nil
}

func (t *sessionTap) Action(ctx context.Context, a agent.ActionRecord) error {
	if a.Tool == "finish" && strings.TrimSpace(a.Output) != "" {
		t.answer = a.Output
	}
	if t.inner != nil {
		return t.inner.Action(ctx, a)
	}
	return nil
}

func (t *sessionTap) FileChange(ctx context.Context, c tools.FileChange) error {
	if t.inner != nil {
		return t.inner.FileChange(ctx, c)
	}
	return nil
}
