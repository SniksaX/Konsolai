package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/safety"
)

const PlanExportReject = "plan must produce the PDF/HTML with the export_doc tool (write Markdown first, then export_doc)"
const PlanCommandReject = "list the exact command lines you will run; for a multi-line script, put the whole script as one command entry"

type proposePlanTool struct {
	docsTask bool
	request  string
}

func newProposePlan(docsTask bool, request string) *proposePlanTool {
	return &proposePlanTool{docsTask: docsTask, request: request}
}

func mentionsDocExport(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "pdf") || strings.Contains(l, "html")
}

func mentionsExportDoc(summary string, steps []string) bool {
	blob := strings.ToLower(summary + " " + strings.Join(steps, " "))
	return strings.Contains(blob, "export_doc")
}

func (t *proposePlanTool) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Type: "function",
		Function: llm.FunctionSpec{
			Name: "propose_plan",
			Description: "Propose a plan before mutating edits or before running commands that need approval. " +
				"Pass a JSON object with summary (string), steps (array of strings), and commands (array of exact shell command lines, one entry per command; put a multi-line script in one entry). " +
				"Include files (repo-relative paths) when the plan creates or edits files. " +
				"Omit files when the plan only runs commands and changes no files, for example go build ./..., go vet ./..., and go test ./.... " +
				"./... is a real Go package pattern, not a placeholder.",
			Parameters: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string","description":"What this plan will do."},"steps":{"type":"array","items":{"type":"string"},"description":"Concrete steps. Use the exact command lines when the plan only runs commands."},"files":{"type":"array","items":{"type":"string"},"description":"Repo-relative paths this plan will create or modify. Required when the plan writes or edits files. Omit when the plan only runs commands and changes no files."},"commands":{"type":"array","items":{"type":"string"},"description":"Exact shell command lines this plan will run, one entry per command. Example: go build ./..., go vet ./..., go test ./.... A multi-line script is one entry. Do not use placeholders."}},"required":["summary","steps"]}`),
		},
	}
}

type proposePlanArgs struct {
	Summary  string   `json:"summary"`
	Steps    []string `json:"steps"`
	Files    []string `json:"files"`
	Commands []string `json:"commands"`
}

type proposePlanAction struct {
	summary  string
	steps    []string
	files    []string
	commands []string
}

func (t *proposePlanTool) Prepare(rawArgs string) (Action, error) {
	var args proposePlanArgs
	dec := json.NewDecoder(strings.NewReader(rawArgs))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return nil, planShapeError("arguments must be a JSON object", rawArgs, proposePlanArgs{})
	}
	if strings.TrimSpace(args.Summary) == "" {
		return nil, planShapeError("summary is required", rawArgs, args)
	}
	if len(nonempty(args.Steps)) < 1 {
		return nil, planShapeError("at least one step is required", rawArgs, args)
	}
	cmds := planCommands(args)
	writes := planWritesFiles(args.Summary, args.Steps, append(append([]string{}, args.Commands...), cmds...))
	if writes && len(nonempty(args.Files)) < 1 {
		return nil, planShapeError("files is required when the plan writes or edits files", rawArgs, args)
	}
	if !writes && len(cmds) == 0 && len(nonempty(args.Files)) < 1 {
		why := "list the exact command lines in commands, or include files when the plan writes or edits files"
		if healthCommands(planTextBlob(rawArgs, args)) != nil {
			why = "list the exact command lines you will run"
		}
		return nil, planShapeError(why, rawArgs, args)
	}
	if t.docsTask && mentionsDocExport(t.request) && !mentionsExportDoc(args.Summary, args.Steps) {
		return nil, planShapeError(PlanExportReject, rawArgs, args)
	}
	for _, c := range args.Commands {
		if placeholderCmd(c) {
			return nil, planShapeError(PlanCommandReject, rawArgs, args)
		}
		if err := safety.PlanCommandForbidden(c); err != nil {
			return nil, planShapeError(err.Error(), rawArgs, args)
		}
	}
	for _, c := range cmds {
		if err := safety.PlanCommandForbidden(c); err != nil {
			return nil, planShapeError(err.Error(), rawArgs, args)
		}
	}
	return &proposePlanAction{summary: args.Summary, steps: args.Steps, files: nonempty(args.Files), commands: cmds}, nil
}

func planCommands(args proposePlanArgs) []string {
	if c := exactCommands(args.Commands); len(c) > 0 {
		return c
	}
	if c := exactCommands(args.Steps); len(c) > 0 && len(c) == len(nonempty(args.Steps)) {
		return c
	}
	return nil
}

func exactCommands(in []string) []string {
	var out []string
	for _, c := range in {
		c = strings.TrimSpace(c)
		if c == "" || placeholderCmd(c) || !commandLine(c) {
			continue
		}
		out = append(out, c)
	}
	return out
}

func commandLine(s string) bool {
	if isGoCheck(s) || safety.IsReadOnlyCommand(s) {
		return true
	}
	if fileEditRE.MatchString(s) || looksLikeProse(s) {
		return false
	}
	fields := strings.Fields(s)
	if len(fields) < 2 || !commandName.MatchString(fields[0]) {
		return false
	}
	return true
}

func looksLikeProse(s string) bool {
	if strings.Contains(s, ": ") {
		return true
	}
	return len(strings.Fields(s)) >= 8
}

func nonempty(in []string) []string {
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func planWritesFiles(summary string, steps, commands []string) bool {
	blob := summary + "\n" + strings.Join(steps, "\n")
	if fileEditRE.MatchString(blob) {
		return true
	}
	for _, c := range commands {
		if commandWritesFiles(c) {
			return true
		}
	}
	return false
}

func commandWritesFiles(cmd string) bool {
	segs := safety.SplitPlanCommand(cmd)
	if len(segs) == 0 {
		return false
	}
	for _, seg := range segs {
		seg = strings.TrimSpace(seg)
		if seg == "" || isGoCheck(seg) || safety.IsReadOnlyCommand(seg) {
			continue
		}
		return true
	}
	return false
}

func isGoCheck(seg string) bool {
	fields := strings.Fields(seg)
	if len(fields) < 2 || filepath.Base(fields[0]) != "go" {
		return false
	}
	switch fields[1] {
	case "build", "vet", "test":
		return true
	default:
		return false
	}
}

var (
	commandName = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)
	fileEditRE  = regexp.MustCompile(`(?i)\b(write_file|edit_file|export_doc)\b|\b(write|edit|create|modify|update|add)\b[^\n]{0,80}\.[A-Za-z0-9]{1,8}\b`)
	goWildcard  = regexp.MustCompile(`(?:\./|\.\./|[A-Za-z0-9_.-]+/)\.\.\.`)
	filePathRE  = regexp.MustCompile(`(?:[\w.-]+/)*[\w.-]+\.[A-Za-z0-9]{1,8}`)
)

func placeholderCmd(s string) bool {
	if strings.Contains(s, "(all") || strings.Contains(s, "…") || strings.Contains(s, "<...>") {
		return true
	}
	if ellipsisPlaceholder(s) {
		return true
	}
	low := strings.ToLower(s)
	for _, w := range strings.Fields(low) {
		w = strings.Trim(w, ".,:;()[]")
		if w == "listed" || w == "each" || w == "etc" {
			return true
		}
	}
	return false
}

func ellipsisPlaceholder(s string) bool {
	if !strings.Contains(s, "...") {
		return false
	}
	return strings.Contains(goWildcard.ReplaceAllString(s, ""), "...")
}

type planExample struct {
	Summary  string   `json:"summary"`
	Steps    []string `json:"steps"`
	Commands []string `json:"commands,omitempty"`
	Files    []string `json:"files,omitempty"`
}

func planShapeError(why, raw string, args proposePlanArgs) error {
	ex, omit := planExampleFor(raw, args)
	omitText := "files may be omitted: no"
	if omit {
		omitText = "files may be omitted: yes"
	}
	return fmt.Errorf("tools: propose_plan: %s\nfields: summary (string), steps (array of strings), commands (array of exact command lines), files (array of repo-relative paths)\n%s\nexample: %s", why, omitText, ex)
}

func planExampleFor(raw string, args proposePlanArgs) (string, bool) {
	text := planTextBlob(raw, args)
	writes := planWritesFiles(args.Summary, args.Steps, args.Commands)
	if args.Summary == "" && len(args.Steps) == 0 && len(args.Commands) == 0 {
		writes = planWritesFiles(raw, nil, nil)
	}
	cmds := healthCommands(text)
	if len(cmds) == 0 {
		cmds = exactCommands(args.Commands)
	}
	if len(cmds) == 0 {
		cmds = exactCommands(args.Steps)
	}
	omit := !writes
	if healthCommands(text) != nil && !fileEditRE.MatchString(text) {
		omit = true
	}
	summary := oneLine(args.Summary)
	if summary == "" && !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		summary = oneLine(raw)
	}
	if summary == "" {
		summary = "Read-only health check of the repo: build, vet, and test status."
	}
	steps := nonempty(args.Steps)
	if len(cmds) > 0 && (len(steps) == 0 || healthCommands(text) != nil) {
		steps = cmds
	}
	if len(steps) == 0 {
		steps = []string{"describe the change"}
	}
	ex := planExample{Summary: summary, Steps: steps, Commands: cmds}
	if !omit {
		files := nonempty(args.Files)
		if len(files) == 0 {
			files = guessFiles(text)
		}
		if len(files) == 0 {
			files = []string{"a.txt"}
		}
		ex.Files = files
	}
	b, err := json.Marshal(ex)
	if err != nil {
		return `{"summary":"Read-only health check","steps":["go build ./...","go vet ./...","go test ./..."],"commands":["go build ./...","go vet ./...","go test ./..."]}`, true
	}
	return string(b), omit
}

func planTextBlob(raw string, args proposePlanArgs) string {
	return raw + "\n" + args.Summary + "\n" + strings.Join(args.Steps, "\n") + "\n" + strings.Join(args.Commands, "\n")
}

func healthCommands(text string) []string {
	l := strings.ToLower(text)
	if strings.Contains(l, "build") && strings.Contains(l, "vet") && strings.Contains(l, "test") {
		return []string{"go build ./...", "go vet ./...", "go test ./..."}
	}
	return nil
}

func guessFiles(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range filePathRE.FindAllString(text, -1) {
		if seen[m] || strings.Contains(m, "...") {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > 180 {
		return string(r[:180])
	}
	return s
}

func (a *proposePlanAction) PlanFiles() []string    { return a.files }
func (a *proposePlanAction) PlanCommands() []string { return a.commands }
func (a *proposePlanAction) PlanText() string {
	var b strings.Builder
	b.WriteString(a.summary)
	b.WriteByte('\n')
	b.WriteString(strings.Join(a.steps, "\n"))
	b.WriteByte('\n')
	b.WriteString(strings.Join(a.files, "\n"))
	b.WriteByte('\n')
	b.WriteString(strings.Join(a.commands, "\n"))
	return b.String()
}

func (a *proposePlanAction) Summary() string { return "plan: " + a.summary }

func (a *proposePlanAction) Preview() string {
	var b strings.Builder
	for i, s := range a.steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, s)
	}
	if len(a.files) > 0 {
		b.WriteString("files: ")
		b.WriteString(strings.Join(a.files, ", "))
		b.WriteByte('\n')
	}
	if len(a.commands) > 0 {
		b.WriteString("commands: ")
		b.WriteString(strings.Join(a.commands, ", "))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func (a *proposePlanAction) Kind() ActionKind { return Plan }

func (a *proposePlanAction) Run(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return Result{Output: "plan approved", Meta: "plan"}, nil
}
