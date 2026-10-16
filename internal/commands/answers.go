package commands

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/memory"
	"github.com/siraj/konsolai/internal/repo"
	"github.com/siraj/konsolai/internal/store"
)

type pair struct {
	Prompt string
	Answer string
}

type fileChange struct {
	Path string
	Diff string
}

type histItem struct {
	ID        string
	Title     string
	Mode      string
	Status    string
	CWD       string
	Age       time.Duration
	Msgs      []llm.Message
	ShellCmds []string
	Changes   []fileChange
}

func parseN(args []string, def, max int) (int, string) {
	if len(args) == 0 {
		return def, ""
	}
	n := 0
	for _, c := range args[0] {
		if c < '0' || c > '9' {
			return 0, "n must be a number"
		}
		n = n*10 + int(c-'0')
	}
	if n < 1 {
		return 0, "n must be at least 1"
	}
	if n > max {
		return 0, "n must be at most " + itoa(max)
	}
	return n, ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func formatSize(n int) string {
	if n < 1024 {
		return itoa(n) + " B"
	}
	return strings.TrimSuffix(strings.TrimSuffix(
		strings.Replace(sprintf1f(float64(n)/1024), ".0", "", 1), "0"), ".") + " KiB"
}

func sprintf1f(f float64) string {
	w := int(f)
	frac := int(f*10+0.5) % 10
	return itoa(w) + "." + itoa(frac)
}

func pairsFromMsgs(msgs []llm.Message) []pair {
	var lastUser string
	var out []pair
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleUser:
			lastUser = m.Content
		case llm.RoleAssistant:
			if strings.TrimSpace(m.Content) != "" && len(m.ToolCalls) == 0 {
				out = append(out, pair{Prompt: lastUser, Answer: m.Content})
			}
		}
	}
	return out
}

func lastN[T any](s []T, n int) []T {
	if n <= 0 || n >= len(s) {
		return s
	}
	return s[len(s)-n:]
}

func projectIDOf(ctx context.Context, env *Env) *int64 {
	if env == nil || env.Mem == nil {
		return nil
	}
	s := env.Mem.Store()
	if s == nil {
		return nil
	}
	root := repo.Root(env.Cwd)
	p := repo.Detect(root)
	id, err := s.UpsertProject(ctx, root, p.Kinds, p.Commands)
	if err != nil {
		return nil
	}
	return &id
}

func loadHistoryFixed(ctx context.Context, env *Env, limit int) []histItem {
	if env != nil && env.LoadHistory != nil {
		return env.LoadHistory(ctx, limit)
	}
	cwd := env.Cwd
	if env.Mem != nil {
		if s := env.Mem.Store(); s != nil {
			var pid *int64
			if id := projectIDOf(ctx, env); id != nil {
				pid = id
			}
			rows, err := s.HistorySessions(ctx, env.Profile.Name, pid, cwd, limit)
			if err != nil {
				env.Mem.Fail(err)
			} else if len(rows) > 0 {
				out := make([]histItem, 0, len(rows))
				for _, sess := range rows {
					msgs, err := s.LoadMessages(ctx, sess.ID)
					if err != nil {
						env.Mem.Fail(err)
						msgs = nil
					}
					out = append(out, histItem{
						ID: sess.ID, Title: sess.Title, Mode: sess.Mode, Status: sess.Status,
						CWD: sess.CWD, Age: time.Since(sess.LastActivity), Msgs: memory.ToLLM(msgs),
						ShellCmds: shellCmdsOf(ctx, s, sess.ID), Changes: changesOf(ctx, s, sess.ID),
					})
				}
				return out
			}
		}
	}
	if sess, ok := memory.LoadLast(env.Profile.Name, cwd, 0); ok {
		age := time.Duration(0)
		if !sess.UpdatedAt.IsZero() {
			age = time.Since(sess.UpdatedAt)
		}
		return []histItem{{
			Title: sess.Title, CWD: sess.CWD, Age: age, Msgs: sess.Messages, Status: "local",
		}}
	}
	return nil
}

func fenceBlocks(text string, langs ...string) []string {
	var out []string
	lines := strings.Split(text, "\n")
	var buf []string
	in := false
	langOK := func(lang string) bool {
		if len(langs) == 0 {
			return true
		}
		lang = strings.ToLower(strings.TrimSpace(lang))
		for _, l := range langs {
			if lang == l {
				return true
			}
		}
		return false
	}
	for _, line := range lines {
		trim := strings.TrimRight(line, "\r")
		if strings.HasPrefix(trim, "```") {
			if !in {
				lang := strings.TrimPrefix(trim, "```")
				if langOK(lang) {
					in = true
					buf = buf[:0]
				}
				continue
			}
			out = append(out, strings.Join(buf, "\n"))
			in = false
			continue
		}
		if in {
			buf = append(buf, trim)
		}
	}
	return out
}

func lastRunShell(ctx context.Context, env *Env) string {
	items := loadHistoryFixed(ctx, env, 1)
	if len(items) == 0 {
		return ""
	}
	if cmds := items[0].ShellCmds; len(cmds) > 0 {
		return cmds[len(cmds)-1]
	}
	if items[0].ID == "" || env.Mem == nil || env.Mem.Store() == nil {
		return ""
	}
	cmds := shellCmdsOf(ctx, env.Mem.Store(), items[0].ID)
	if len(cmds) == 0 {
		return ""
	}
	return cmds[len(cmds)-1]
}

func shellCmdsOf(ctx context.Context, s *store.Store, sessionID string) []string {
	if s == nil || sessionID == "" {
		return nil
	}
	acts, err := s.ListActions(ctx, sessionID)
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range acts {
		if a.Tool != "run_shell" {
			continue
		}
		var args struct {
			Cmd string `json:"cmd"`
		}
		if json.Unmarshal(a.Args, &args) == nil && args.Cmd != "" {
			out = append(out, args.Cmd)
		}
	}
	return out
}

func changesOf(ctx context.Context, s *store.Store, sessionID string) []fileChange {
	if s == nil || sessionID == "" {
		return nil
	}
	rows, err := s.ListFileChanges(ctx, sessionID)
	if err != nil {
		return nil
	}
	out := make([]fileChange, 0, len(rows))
	for _, c := range rows {
		out = append(out, fileChange{Path: c.Path, Diff: c.Diff})
	}
	return out
}

func ageLabel(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return itoa(int(d.Minutes())) + "m"
	}
	if d < 24*time.Hour {
		return itoa(int(d.Hours())) + "h"
	}
	return itoa(int(d.Hours()/24)) + "d"
}

func clipTitle(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > 48 {
		return string([]rune(s)[:48]) + "…"
	}
	return s
}
