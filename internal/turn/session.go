package turn

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SniksaX/Konsolai/internal/agent"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/intent"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/memory"
	"github.com/SniksaX/Konsolai/internal/repo"
	"github.com/SniksaX/Konsolai/internal/store"
	"github.com/SniksaX/Konsolai/internal/tools"
	"github.com/SniksaX/Konsolai/internal/ui"
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
		Mode: mode, Model: prof.MainModelID(), Title: title,
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

func DecideIntentFloor(ctx context.Context, client *llm.Client, forceIntent, request string, newSession bool, cands []intent.Candidate, inRepo bool, verbs, targets []string, prev string) intent.Decision {
	floor, hasFloor := intent.ActionFloorPrev(request, prev, verbs, targets, inRepo)
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
	// The identity floor answers "what model are you" from the configured
	// facts without a model call, so no model can guess its own identity.
	if identityFloor(request) {
		return intent.Decision{
			Intent: intent.Answer, Language: identityLanguage(nil, request),
			Reason: "identity floor", Effort: intent.EffortLow, Task: intent.TaskOther,
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

// identityPhrases are the folded shapes of questions that ask which model is
// answering. They are answered from the configured facts, never generated.
var identityPhrases = []string{
	"what model are you", "which model are you",
	"what model is this", "which model is this",
	"what model am i using", "which model am i using",
	"what model are we using", "what model are you using",
	"what model is answering", "which model is answering",
	"what model is serving", "which model is serving",
	"what model do you use", "what model does this use",
	"what model powers you", "what llm are you",
	"what llm is this", "which llm is this",
	"who are you",
	"quel modele", "quelle modele", "quel modele es tu",
	"tu es quel modele", "c est quel modele",
	"modele utilises tu", "tu utilises quel modele",
}

var identityAccents = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"î", "i", "ï", "i", "ô", "o", "ö", "o", "ù", "u", "û", "u", "ü", "u", "ç", "c",
)

// identityFloor reports whether text asks which model is answering.
func identityFloor(text string) bool {
	folded := " " + foldIdentity(text) + " "
	for _, phrase := range identityPhrases {
		if strings.Contains(folded, " "+phrase+" ") {
			return true
		}
	}
	return false
}

func foldIdentity(text string) string {
	s := identityAccents.Replace(strings.ToLower(text))
	var b strings.Builder
	space := false
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			space = false
			continue
		}
		if !space && b.Len() > 0 {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// identityLanguage picks the language of the deterministic identity answer.
func identityLanguage(r *Runner, text string) string {
	if r != nil {
		switch strings.ToLower(strings.TrimSpace(r.Profile.Language.Mode)) {
		case "en", "fr":
			return strings.ToLower(strings.TrimSpace(r.Profile.Language.Mode))
		}
	}
	if lang := intent.Detect(text, nil); lang != "" {
		return lang
	}
	if r != nil {
		if def := strings.ToLower(strings.TrimSpace(r.Profile.Language.Default)); def != "" {
			return def
		}
	}
	return "en"
}

func identityModel(name, id, host string, remote bool, lang string) string {
	fr := lang == "fr"
	if strings.TrimSpace(name) == "" {
		name = "default"
	}
	if strings.TrimSpace(id) == "" {
		if fr {
			id = "par défaut du serveur"
		} else {
			id = "server default"
		}
	}
	if strings.TrimSpace(host) == "" {
		if fr {
			host = "non configuré"
		} else {
			host = "not configured"
		}
	}
	loc := "local"
	if remote {
		if fr {
			loc = "distant"
		} else {
			loc = "remote"
		}
	}
	if fr {
		return fmt.Sprintf("%s (id : %s) sur %s, %s", name, id, host, loc)
	}
	return fmt.Sprintf("%s (id: %s) at %s, %s", name, id, host, loc)
}

// identityAnswer renders the configured models without calling a model.
func (r *Runner) identityAnswer(lang string) string {
	facts := r.modelFacts("")
	main := identityModel(facts.MainName, facts.MainID, facts.MainHost, facts.MainRemote, lang)
	var b strings.Builder
	if lang == "fr" {
		b.WriteString("Je suis Konsolai, un agent de codage local. Les modèles configurés sont :\n")
		fmt.Fprintf(&b, "- rôle main : %s\n", main)
		if facts.HasFast {
			fmt.Fprintf(&b, "- rôle fast : %s\n", identityModel(facts.FastName, facts.FastID, facts.FastHost, facts.FastRemote, lang))
		} else {
			b.WriteString("- rôle fast : non configuré ; les réponses passent par main\n")
		}
		b.WriteString("Cette réponse vient des faits de configuration, sans appel au modèle.")
		return b.String()
	}
	b.WriteString("I'm Konsolai, a local coding agent. The configured models are:\n")
	fmt.Fprintf(&b, "- main role: %s\n", main)
	if facts.HasFast {
		fmt.Fprintf(&b, "- fast role: %s\n", identityModel(facts.FastName, facts.FastID, facts.FastHost, facts.FastRemote, lang))
	} else {
		b.WriteString("- fast role: not configured; answers use main\n")
	}
	b.WriteString("This answer comes from the configured model facts, not from a model call.")
	return b.String()
}

// answerIdentityTurn answers an identity question from the configured facts,
// persists the exchange like any answer, and makes no model call.
func (r *Runner) answerIdentityTurn(ctx context.Context, req Request, cwd string, mem *memory.Guard, projectID *int64) Result {
	lang := identityLanguage(r, req.Text)
	answer := r.identityAnswer(lang)
	sessionID := req.ResumeSession
	if sessionID == "" {
		sessionID = CreateSession(ctx, mem, r.Profile, projectID, cwd, string(intent.Answer), req.Text)
	}
	rec := memory.NewRecorder(mem, sessionID, cwd)
	user := llm.Message{Role: llm.RoleUser, Content: req.Text}
	assistant := llm.Message{Role: llm.RoleAssistant, Content: answer}
	_ = rec.Message(ctx, user)
	_ = rec.Message(ctx, assistant)
	if sessionID != "" && !req.KeepOpen {
		if st := mem.Store(); st != nil {
			endCtx, cancel := mem.Isolated(ctx)
			defer cancel()
			_ = st.EndSession(endCtx, sessionID, SessionStatus(0))
		}
	}
	if mem.Store() == nil {
		title := req.Text
		if utf8.RuneCountInString(title) > 80 {
			title = string([]rune(title)[:80])
		}
		_ = memory.SaveLast(memory.LastSession{
			Profile: r.Profile.Name, CWD: cwd, Title: title,
			Messages: []llm.Message{user, assistant},
		})
	}
	if req.Trace || r.Profile.UI.Trace {
		fmt.Fprintln(r.errw(), "identity: answered from the configured model facts (no model call)")
	}
	r.lastCode = 0
	r.lastIntent = intent.Answer
	r.lastLang = lang
	r.lastEffort = intent.EffortLow
	r.lastTask = intent.TaskOther
	r.lastUser = req.Text
	r.lastAssistant = answer
	r.lastPromise = false
	r.Serving = agent.Model{Name: "facts", Role: "local"}
	r.LastWindow = 0
	r.LastPromptTokens = 0
	ui.BeforeOutput()
	if req.Plain {
		fmt.Fprintln(r.out(), answer)
	} else {
		rend := ui.NewRenderer(ui.NewIndentWriter(r.out(), 4), false)
		_, _ = rend.Write([]byte(answer))
		_ = rend.Flush()
	}
	return Result{SessionID: sessionID, Intent: string(intent.Answer), ExitCode: 0, Answer: answer}
}
