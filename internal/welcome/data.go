// Package welcome gathers the startup snapshot. Rendering never waits on it
// past the deadline; late probe results are dropped.
package welcome

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/siraj/konsolai/internal/banner"
	"github.com/siraj/konsolai/internal/config"
	"github.com/siraj/konsolai/internal/store"
)

type Status string

const (
	StatusUp      Status = "up"      // latency under the slow threshold
	StatusSlow    Status = "slow"    // latency >= threshold (embed: latency > threshold)
	StatusDown    Status = "down"    // error
	StatusTimeout Status = "timeout" // deadline hit
)

type Model struct {
	Name   string // config model name
	Role   string // main, fast, embed, rerank
	Status Status
	Stat   string // already formatted for the stat column
}

type Session struct {
	Dir     string // ~-shortened working dir (existing helper)
	Branch  string // branch name, or "not a git repo"
	Dirty   bool
	Changed int
	Ahead   int
	Behind  int
	Build   string // parsed git describe (existing helper)
	Memory  string // "N facts · M sessions" or "memory offline"
}

type Recent struct {
	Title  string
	Intent string
	Age    string // 5m, 2h, 1d, 3w
}

type Data struct {
	Host        string  // main endpoint hostname only, e.g. fatboy (box title)
	Models      []Model // always 4 when config has them: main, fast, embed, rerank
	Up          int
	Total       int
	Session     Session
	Recent      []Recent // last 4; empty means no sessions
	Placeholder string
	Tip         string
	Version     string // existing version string, frame title uses konsolai + version
	Limits      Limits // paint-time layout cuts; Gather leaves this zero
}

// Limits are the welcome size cuts. Zero fields keep today's layout.
type Limits struct {
	TinyHeight   int
	RecentHeight int
	TinyWidth    int
	NarrowWidth  int
	FrameWidth   int
}

// Input is everything Gather needs. Tests inject endpoints, a store, and Now.
type Input struct {
	Endpoint       string
	Model          string
	FastEndpoint   string
	FastModel      string
	EmbedEndpoint  string
	RerankEndpoint string
	Profile        string
	ProjectID      *int64
	Store          *store.Store
	CWD            string
	Version        string
	Now            func() time.Time
	// Zero durations and RateWindow keep the built-in welcome budgets.
	ProbeBudget   time.Duration
	ProbeTimeout  time.Duration
	GitTimeout    time.Duration
	MemoryTimeout time.Duration
	RateWindow    int
	ModelSlow     time.Duration
}

// resolveInput fills zero gather budgets from the built-in profile.
// Callers that already resolved a profile pass those values through.
func resolveInput(in Input) Input {
	b := config.Builtin()
	if in.ProbeBudget <= 0 {
		in.ProbeBudget = b.ProbeDeadline()
	}
	if in.ProbeTimeout <= 0 {
		in.ProbeTimeout = b.ProbeTimeout()
	}
	if in.GitTimeout <= 0 {
		in.GitTimeout = b.GitQueryTimeout()
	}
	if in.MemoryTimeout <= 0 {
		in.MemoryTimeout = b.MemoryQueryTimeout()
	}
	if in.RateWindow <= 0 {
		in.RateWindow = b.ModelRateWindow()
	}
	if in.ModelSlow <= 0 {
		in.ModelSlow = b.ModelSlow()
	}
	return in
}

// Gather fills one Data concurrently. The wait never exceeds ProbeBudget
// (the built-in probe deadline when that field is zero). Whatever has not
// arrived stays degraded. A non-nil error is not used for probe or database
// failures — those stay in the returned Data.
func Gather(ctx context.Context, in Input) (Data, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	in = resolveInput(in)
	ctx, cancel := context.WithTimeout(ctx, in.ProbeBudget)
	defer cancel()

	nowFn := in.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()

	models, specs := configuredProbes(in)
	g := &gate{d: Data{
		Host:        banner.HostOf(in.Endpoint),
		Version:     in.Version,
		Placeholder: "ask anything",
		Tip:         tipFor(now),
		Session: Session{
			Dir:    shortDir(in.CWD),
			Build:  banner.ParseBuild(in.Version),
			Memory: "memory offline",
		},
		Models: models,
		Total:  len(models),
	}}

	var wg sync.WaitGroup
	run := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { _ = recover() }()
			fn()
		}()
	}
	for _, sp := range specs {
		sp := sp
		run(func() {
			g.setModel(sp.i, probeModel(ctx, in, sp.role, sp.name, sp.ep, sp.kind))
		})
	}
	run(func() { loadGit(ctx, in.CWD, g, in.GitTimeout) })
	run(func() { loadMemory(ctx, in, g, now) })

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}

	g.mu.Lock()
	g.closed = true
	d := g.d
	g.mu.Unlock()

	d.Total = len(d.Models)
	d.Up = 0
	// slow counts as up: a slow server answered.
	for _, m := range d.Models {
		if m.Status == StatusUp || m.Status == StatusSlow {
			d.Up++
		}
	}
	if d.Placeholder == "" {
		d.Placeholder = "ask anything"
	}
	if d.Session.Memory == "" {
		d.Session.Memory = "memory offline"
	}
	if d.Tip == "" {
		d.Tip = tipFor(now)
	}
	return d, nil
}

type gate struct {
	mu     sync.Mutex
	closed bool
	d      Data
}

func (g *gate) setModel(i int, m Model) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || i < 0 || i >= len(g.d.Models) {
		return
	}
	g.d.Models[i] = m
}

type probeSpec struct {
	i    int
	role string
	name string
	ep   string
	kind string
}

func configuredProbes(in Input) ([]Model, []probeSpec) {
	var models []Model
	var specs []probeSpec
	add := func(role, name, ep, kind string) {
		if strings.TrimSpace(ep) == "" {
			return
		}
		specs = append(specs, probeSpec{i: len(models), role: role, name: name, ep: ep, kind: kind})
		models = append(models, Model{Name: name, Role: role, Status: StatusTimeout, Stat: "timeout"})
	}
	add("main", in.Model, in.Endpoint, "models")
	add("fast", in.FastModel, in.FastEndpoint, "models")
	add("embed", "", in.EmbedEndpoint, "embed")
	add("rerank", "", in.RerankEndpoint, "rerank")
	return models, specs
}

func probeModel(ctx context.Context, in Input, role, name, endpoint, kind string) Model {
	m := Model{Name: name, Role: role, Status: StatusTimeout, Stat: "timeout"}
	if strings.TrimSpace(endpoint) == "" {
		m.Status = StatusDown
		m.Stat = "n/a"
		return m
	}
	timeout := in.ProbeTimeout
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	err := doProbe(pctx, endpoint, kind, timeout)
	lat := time.Since(start)
	if err != nil {
		if timedOut(err, pctx) {
			return m
		}
		m.Status = StatusDown
		if p := portOf(endpoint); p != "" {
			m.Stat = ":" + p + " unreachable"
		} else {
			m.Stat = "n/a"
		}
		return m
	}
	m.Status = statusForLimit(role, lat, in.ModelSlow)
	m.Stat = statText(ctx, in, role, name, lat)
	return m
}

func statusFor(role string, lat time.Duration) Status {
	return statusForLimit(role, lat, config.Builtin().ModelSlow())
}

func statusForLimit(role string, lat, slow time.Duration) Status {
	if role == "embed" {
		if lat > slow {
			return StatusSlow
		}
		return StatusUp
	}
	if lat >= slow {
		return StatusSlow
	}
	return StatusUp
}

func statText(ctx context.Context, in Input, role, name string, lat time.Duration) string {
	if role != "embed" && in.Store != nil && name != "" {
		lastN := in.RateWindow
		rate, err := in.Store.ModelRate(ctx, name, lastN)
		if err == nil && rate != nil {
			return fmt.Sprintf("%.0f tokens/s", *rate)
		}
	}
	if lat < 0 {
		return "n/a"
	}
	return fmt.Sprintf("%dms", nonNegMS(lat))
}

func nonNegMS(d time.Duration) int64 {
	ms := d.Milliseconds()
	if ms < 0 {
		return 0
	}
	return ms
}

func doProbe(ctx context.Context, endpoint, kind string, timeout time.Duration) error {
	var (
		req *http.Request
		err error
	)
	switch kind {
	case "embed":
		raw := strings.TrimRight(strings.TrimSpace(endpoint), "/") + "/embed"
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, raw, bytes.NewReader([]byte(`{"inputs":["ok"]}`)))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	case "rerank":
		raw := strings.TrimRight(strings.TrimSpace(endpoint), "/") + "/info"
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	default:
		raw := modelsURL(endpoint)
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	}
	if err != nil {
		return fmt.Errorf("welcome: probe request: %w", err)
	}
	resp, err := probeHTTP(timeout).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("welcome: probe status %d", resp.StatusCode)
	}
	return nil
}

func probeHTTP(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{}
	tr := &http.Transport{
		DialContext:           dialer.DialContext,
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: timeout,
		TLSHandshakeTimeout:   timeout,
	}
	return &http.Client{Timeout: timeout, Transport: tr}
}

func modelsURL(endpoint string) string {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if strings.HasSuffix(endpoint, "/v1") {
		return endpoint + "/models"
	}
	return endpoint + "/v1/models"
}

func portOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	if p := u.Port(); p != "" {
		return p
	}
	switch u.Scheme {
	case "https":
		return "443"
	case "http":
		if u.Host != "" {
			return "80"
		}
	}
	return ""
}

func timedOut(err error, ctx context.Context) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return ctx != nil && ctx.Err() != nil
}

func loadGit(ctx context.Context, cwd string, g *gate, timeout time.Duration) {
	branch, dirty, changed, ahead, behind, notRepo := gitStatus(ctx, cwd, timeout)
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return
	}
	if notRepo {
		g.d.Session.Branch = "not a git repo"
		return
	}
	g.d.Session.Branch = branch
	g.d.Session.Dirty = dirty
	g.d.Session.Changed = changed
	g.d.Session.Ahead = ahead
	g.d.Session.Behind = behind
}

func gitStatus(ctx context.Context, cwd string, timeout time.Duration) (branch string, dirty bool, changed, ahead, behind int, notRepo bool) {
	if strings.TrimSpace(cwd) == "" {
		return "not a git repo", false, 0, 0, 0, true
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", "-C", cwd, "status", "--porcelain=v2", "--branch")
	out, err := cmd.Output()
	if cctx.Err() != nil {
		return "", false, 0, 0, 0, false
	}
	if err != nil {
		return "not a git repo", false, 0, 0, 0, true
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "# branch.head "); ok {
			branch = strings.TrimSpace(rest)
			continue
		}
		if rest, ok := strings.CutPrefix(line, "# branch.ab "); ok {
			ahead, behind = parseAB(rest)
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		changed++
	}
	return branch, changed > 0, changed, ahead, behind, false
}

func parseAB(rest string) (ahead, behind int) {
	for _, f := range strings.Fields(rest) {
		if strings.HasPrefix(f, "+") {
			n, err := strconv.Atoi(f[1:])
			if err == nil {
				ahead = n
			}
		} else if strings.HasPrefix(f, "-") {
			n, err := strconv.Atoi(f[1:])
			if err == nil {
				behind = n
			}
		}
	}
	return ahead, behind
}

func loadMemory(ctx context.Context, in Input, g *gate, now time.Time) {
	if in.Store == nil || g == nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, in.MemoryTimeout)
	defer cancel()
	facts, sessions, err := in.Store.WelcomeMemory(cctx, in.Profile, in.ProjectID)
	if err != nil {
		return
	}
	mem := fmt.Sprintf("%d facts · %d sessions", facts, sessions)
	rows, latest, ok, rerr := in.Store.WelcomeRecent(cctx, in.Profile, in.CWD, 4)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return
	}
	g.d.Session.Memory = mem
	if rerr != nil {
		return
	}
	g.d.Recent = recentFrom(rows, now)
	g.d.Placeholder = resumePlaceholder(latest, ok, in.CWD, now)
}

func recentFrom(rows []store.WelcomeRow, now time.Time) []Recent {
	if len(rows) == 0 {
		return nil
	}
	if len(rows) > 4 {
		rows = rows[:4]
	}
	out := make([]Recent, 0, len(rows))
	for _, row := range rows {
		out = append(out, Recent{
			Title:  displayTitle(row.Title, row.FirstUser),
			Intent: row.Intent,
			Age:    ageLabel(now.Sub(row.LastAt)),
		})
	}
	return out
}

func resumePlaceholder(row store.WelcomeRow, ok bool, cwd string, now time.Time) string {
	if !ok || !sameDir(row.CWD, cwd) || now.Sub(row.LastAt) >= 24*time.Hour {
		return "ask anything"
	}
	title := displayTitle(row.Title, row.FirstUser)
	if title == "" {
		title = "session"
	}
	return "ask anything, or /resume to continue " + title
}

func displayTitle(title, firstUser string) string {
	s := strings.TrimSpace(title)
	if s == "" {
		s = strings.TrimSpace(firstUser)
	}
	s = strings.Join(strings.Fields(s), " ")
	return clipTitle(s)
}

func clipTitle(s string) string {
	const max = 48
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 3 {
		return "..."
	}
	return string(r[:max-3]) + "..."
}

func ageLabel(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	default:
		return fmt.Sprintf("%dw", int(d/(7*24*time.Hour)))
	}
}

func sameDir(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func shortDir(cwd string) string {
	if cwd == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		if cwd == home {
			return "~"
		}
		if strings.HasPrefix(cwd, home+string(filepath.Separator)) {
			return "~" + cwd[len(home):]
		}
	}
	return cwd
}

var tips = []string{
	"/undo reverts the last file change",
	"@file attaches a file to the turn",
	"!cmd runs a shell command",
	"/resume continues a recent session",
	"/index refreshes the project index",
	"/theme switches the color palette",
	"/copy code copies the last code block",
	"/run repeats a shell command",
	"/recall searches memory",
	"/help lists every command",
	"/clear redraws the compact header",
	"/new starts a fresh session",
}

func tipFor(now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	if len(tips) == 0 {
		return ""
	}
	return tips[now.YearDay()%len(tips)]
}
