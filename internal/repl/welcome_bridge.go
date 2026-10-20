package repl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/SniksaX/Konsolai/internal/banner"
	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/memory"
	"github.com/SniksaX/Konsolai/internal/store"
	"github.com/SniksaX/Konsolai/internal/ui"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
	"github.com/SniksaX/Konsolai/internal/welcome"
	"golang.org/x/term"
)

const fallbackPlaceholder = "ask anything"

func builtinGatherLimit() time.Duration { return config.Builtin().ProbeDeadline() }
func builtinReadyLimit() time.Duration  { return config.Builtin().StartupCap() }
func builtinFreshFor() time.Duration    { return config.Builtin().WelcomeCacheFor() }

// gatherData is welcome.Gather. Tests replace it to prove a stale refresh
// does not block the redraw.
var gatherData = welcome.Gather

type welcomeSnap struct {
	info     banner.Info
	data     welcome.Data
	layout   string
	wordmark bool
	at       time.Time
	in       welcome.Input
	limits   welcome.Limits
}

type welcomeState struct {
	mu            sync.Mutex
	snap          welcomeSnap
	onPlaceholder func(string)
	gatherLimit   time.Duration
	freshFor      time.Duration
	rows          int
}

func (s *welcomeState) retarget(cwd, dir, branch string, projectID *int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.in.CWD = cwd
	s.snap.in.ProjectID = projectID
	s.snap.info.Dir = dir
	s.snap.info.Branch = branch
	s.snap.data.Session.Dir = dir
	s.snap.data.Session.Branch = branch
}

func (s *welcomeState) snapshot() welcomeSnap {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snap
}

func (s *welcomeState) placeholder() string {
	ph := s.snapshot().data.Placeholder
	if ph == "" {
		return fallbackPlaceholder
	}
	return ph
}

func termBox(w io.Writer) (width, height int) {
	width = theme.TermWidth()
	height = 24
	if ls := os.Getenv("LINES"); ls != "" {
		if n, err := strconv.Atoi(ls); err == nil && n > 0 {
			height = n
		}
	}
	f := termFile(w)
	if f == nil {
		if width < 1 {
			width = 80
		}
		if height < 1 {
			height = 24
		}
		return width, height
	}
	c, r, err := term.GetSize(int(f.Fd()))
	if err != nil {
		if width < 1 {
			width = 80
		}
		return width, height
	}
	if os.Getenv("COLUMNS") == "" && c > 0 {
		width = c
	}
	if os.Getenv("LINES") == "" && r > 0 {
		height = r
	}
	if width < 1 {
		width = 80
	}
	if height < 1 {
		height = 24
	}
	return width, height
}

// termFile finds the terminal behind the REPL writers. paintWelcome is given
// the newline-tracking wrapper around stdout, which is not itself an *os.File.
func termFile(w io.Writer) *os.File {
	for i := 0; i < 8 && w != nil; i++ {
		switch v := w.(type) {
		case *os.File:
			return v
		case *nlWriter:
			w = v.w
		default:
			u, ok := w.(interface{ Unwrap() io.Writer })
			if !ok {
				return nil
			}
			next := u.Unwrap()
			if next == w {
				return nil
			}
			w = next
		}
	}
	return nil
}

// awaitStartup waits until the banner draw has finished and welcome data is
// ready. Data is abandoned at the gather budget. The whole wait stops at the
// startup cap so a stuck banner or an uncancellable DNS lookup cannot hold
// the prompt.
func awaitStartup(data <-chan welcome.Data, bannerDone <-chan struct{}) (welcome.Data, bool) {
	return awaitStartupLimits(data, bannerDone, builtinGatherLimit(), builtinReadyLimit())
}

func awaitStartupLimits(data <-chan welcome.Data, bannerDone <-chan struct{}, gather, ready time.Duration) (welcome.Data, bool) {
	if gather <= 0 {
		gather = builtinGatherLimit()
	}
	if ready <= 0 {
		ready = builtinReadyLimit()
	}
	total := time.NewTimer(ready)
	defer total.Stop()
	dataTimer := time.NewTimer(gather)
	defer dataTimer.Stop()

	var view welcome.Data
	got, banner := false, false
	for !(got && banner) {
		select {
		case v, ok := <-data:
			if ok && !got {
				view = v
				got = true
				data = nil
			}
		case <-bannerDone:
			banner = true
			bannerDone = nil
		case <-dataTimer.C:
			if !got {
				got = true
			}
		case <-total.C:
			return view, got
		}
	}
	return view, true
}

func welcomeInput(env *commands.Env, info banner.Info) welcome.Input {
	in := welcome.Input{
		Model:   info.Model,
		Version: info.Version,
		CWD:     info.Dir,
	}
	if env == nil {
		return in
	}
	in.Endpoint = env.Profile.MainEndpoint()
	in.Model = firstNonEmpty(info.Model, env.Profile.MainModelID())
	in.FastEndpoint = env.Profile.FastEndpoint()
	in.FastModel = env.Profile.FastModelID()
	in.EmbedEndpoint = env.Profile.EmbedURL()
	in.RerankEndpoint = env.Profile.RerankURL()
	in.Profile = env.Profile.Name
	in.ProjectID = env.ProjectID
	in.CWD = env.Cwd
	if env.Cwd == "" {
		in.CWD = info.Dir
	}
	in.Store = storeOf(env.Mem)
	in.ProbeBudget = env.Profile.ProbeDeadline()
	in.ProbeTimeout = env.Profile.ProbeTimeout()
	in.GitTimeout = env.Profile.GitQueryTimeout()
	in.MemoryTimeout = env.Profile.MemoryQueryTimeout()
	in.RateWindow = env.Profile.ModelRateWindow()
	in.ModelSlow = env.Profile.ModelSlow()
	return in
}

func welcomeLimits(env *commands.Env) welcome.Limits {
	if env == nil {
		return welcome.Limits{}
	}
	p := env.Profile
	return welcome.Limits{
		TinyHeight:   p.LayoutTinyHeight(),
		RecentHeight: p.LayoutRecentHeight(),
		TinyWidth:    p.LayoutTinyWidth(),
		NarrowWidth:  p.LayoutNarrowWidth(),
		FrameWidth:   p.LayoutFrameWidth(),
	}
}

func (s *welcomeState) gatherFor() time.Duration {
	if s != nil && s.gatherLimit > 0 {
		return s.gatherLimit
	}
	return builtinGatherLimit()
}

func (s *welcomeState) cacheFor() time.Duration {
	if s != nil && s.freshFor > 0 {
		return s.freshFor
	}
	return builtinFreshFor()
}

func storeOf(mem *memory.Guard) *store.Store {
	if mem == nil {
		return nil
	}
	return mem.Store()
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func degradedWelcome(info banner.Info) welcome.Data {
	d := welcome.Data{
		Host:        info.Host,
		Version:     info.Version,
		Placeholder: fallbackPlaceholder,
		Session: welcome.Session{
			Dir:    info.Dir,
			Branch: info.Branch,
			Build:  banner.ParseBuild(info.Version),
			Memory: "memory offline",
		},
	}
	if info.Model != "" {
		d.Models = []welcome.Model{{
			Name: info.Model, Role: "main", Status: welcome.StatusTimeout, Stat: "timeout",
		}}
		d.Total = 1
	}
	return d
}

func mergeWelcome(d welcome.Data, info banner.Info) welcome.Data {
	if d.Placeholder == "" {
		d.Placeholder = fallbackPlaceholder
	}
	if d.Version == "" {
		d.Version = info.Version
	}
	if d.Host == "" {
		d.Host = info.Host
	}
	if d.Session.Dir == "" {
		d.Session.Dir = info.Dir
	}
	if d.Session.Branch == "" && info.Branch != "" {
		d.Session.Branch = info.Branch
	}
	if d.Session.Memory == "" {
		d.Session.Memory = "memory offline"
	}
	if len(d.Models) == 0 && info.Model != "" {
		d.Models = []welcome.Model{{
			Name: info.Model, Role: "main", Status: welcome.StatusTimeout, Stat: "timeout",
		}}
		d.Total = 1
	}
	return d
}

func openWelcome(ctx context.Context, w io.Writer, env *commands.Env, info banner.Info, tty, wordmark bool) (*welcomeState, error) {
	layout := "split"
	gatherFor := builtinGatherLimit()
	readyFor := builtinReadyLimit()
	freshFor := builtinFreshFor()
	if env != nil {
		layout = env.Profile.WelcomeLayout()
		gatherFor = env.Profile.ProbeDeadline()
		readyFor = env.Profile.StartupCap()
		freshFor = env.Profile.WelcomeCacheFor()
	}
	limits := welcomeLimits(env)
	if limits.TinyWidth > 0 {
		info.MinWidth = limits.TinyWidth
	}
	in := welcomeInput(env, info)
	st := &welcomeState{
		gatherLimit: gatherFor,
		freshFor:    freshFor,
		snap: welcomeSnap{
			info: info, layout: layout, wordmark: wordmark, in: in,
			data: degradedWelcome(info), limits: limits,
		},
	}
	if !tty {
		if err := banner.Startup(w, info, wordmark, false, ui.Current(), theme.TermWidth()); err != nil {
			return nil, err
		}
		st.snap.at = time.Now()
		return st, nil
	}

	gctx, cancel := context.WithTimeout(ctx, st.gatherFor())
	dataCh := make(chan welcome.Data, 1)
	go func() {
		defer cancel()
		d, err := gatherData(gctx, in)
		if err != nil {
			d = degradedWelcome(info)
		}
		dataCh <- mergeWelcome(d, info)
	}()
	bannerDone := make(chan struct{})
	var berr error
	go func() {
		defer close(bannerDone)
		berr = banner.Startup(w, info, wordmark, true, ui.Current(), theme.TermWidth())
	}()
	view, ok := awaitStartupLimits(dataCh, bannerDone, st.gatherFor(), readyFor)
	select {
	case <-bannerDone:
		if berr != nil {
			return nil, berr
		}
	default:
	}
	if !ok {
		view = degradedWelcome(info)
	}
	view = mergeWelcome(view, info)
	st.mu.Lock()
	st.snap.data = view
	st.snap.at = time.Now()
	st.mu.Unlock()
	// The interim banner was the splash while probes ran. The welcome render
	// includes its own wordmark and the panels, so replace that splash once.
	fmt.Fprint(w, theme.ClearHome)
	st.rows = writeWelcome(w, st.snapshot())
	return st, nil
}

func paintWelcome(w io.Writer, snap welcomeSnap) {
	width, height := termBox(w)
	d := snap.data
	d.Limits = snap.limits
	welcome.Render(w, d, snap.layout, width, height)
}

func writeWelcome(w io.Writer, snap welcomeSnap) int {
	var buf bytes.Buffer
	paintWelcome(&buf, snap)
	_, _ = w.Write(buf.Bytes())
	return paintedRows(buf.Bytes())
}

func paintedRows(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	n := bytes.Count(b, []byte("\n"))
	if !bytes.HasSuffix(b, []byte("\n")) {
		n++
	}
	return n
}

// redraw paints the full welcome from the cached startup data. A cache older
// than the welcome cache window refreshes in the background and does not
// delay this paint. The return value is the number of rows painted.
func (s *welcomeState) redraw(w io.Writer) int {
	if s == nil {
		return 0
	}
	if w == nil {
		w = os.Stdout
	}
	snap := s.snapshot()
	if !snap.at.IsZero() && time.Since(snap.at) > s.cacheFor() {
		go s.refresh()
	}
	n := writeWelcome(w, snap)
	s.mu.Lock()
	s.rows = n
	s.mu.Unlock()
	return n
}

func (s *welcomeState) refresh() {
	s.mu.Lock()
	in := s.snap.in
	info := s.snap.info
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), s.gatherFor())
	defer cancel()
	d, err := gatherData(ctx, in)
	if err != nil {
		return
	}
	d = mergeWelcome(d, info)
	s.mu.Lock()
	s.snap.data = d
	s.snap.at = time.Now()
	fn := s.onPlaceholder
	ph := d.Placeholder
	s.mu.Unlock()
	if fn != nil && ph != "" {
		fn(ph)
	}
}

func bindWelcome(st *welcomeState, rd LineReader) {
	if st == nil {
		return
	}
	setPH := func(s string) {
		if sp, ok := rd.(interface{ SetPlaceholder(string) }); ok {
			sp.SetPlaceholder(s)
		}
	}
	setPH(st.placeholder())
	st.mu.Lock()
	st.onPlaceholder = setPH
	st.mu.Unlock()
}
