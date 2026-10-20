package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/store"
	"github.com/SniksaX/Konsolai/internal/ui"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
	"golang.org/x/term"
)

const (
	chromeFix = "install chromium (sudo pacman -S chromium) or set KONSOLAI_CHROME"
)

var doctorChromeBins = []string{
	"chromium",
	"chromium-browser",
	"google-chrome-stable",
	"google-chrome",
	"brave",
	"brave-browser",
}

var (
	doctorLookPath = exec.LookPath
	doctorHome     = os.UserHomeDir
	doctorTermSize = defaultTermSize
)

type doctorCheck struct {
	mark   string
	name   string
	detail string
	fix    string
}

func runDoctor(ctx context.Context, env *Env, _ []string) int {
	if env == nil {
		return 1
	}
	fail := 0
	for _, c := range doctorChecks(ctx, env) {
		writeDoctor(env.out(), c)
		if c.mark == "✗" {
			fail++
		}
	}
	if fail > 0 {
		return 1
	}
	return 0
}

func doctorChecks(ctx context.Context, env *Env) []doctorCheck {
	timeout := env.Profile.ProbeTimeout()
	file, mode := checkConfigFile(env)
	out := []doctorCheck{file, mode}
	out = append(out, checkModels(ctx, "main endpoint", env.Profile.MainEndpoint(), "roles.main", timeout))
	if env.Profile.FastOn() {
		out = append(out, checkModels(ctx, "fast endpoint", env.Profile.FastEndpoint(), "roles.fast", timeout))
	}
	if strings.TrimSpace(env.Profile.EmbedURL()) != "" {
		out = append(out, checkKind(ctx, "embed endpoint", env.Profile.EmbedURL(), "embed", "roles.embed", timeout))
	}
	if strings.TrimSpace(env.Profile.RerankURL()) != "" {
		out = append(out, checkKind(ctx, "rerank endpoint", env.Profile.RerankURL(), "rerank", "roles.rerank", timeout))
	}
	out = append(out,
		checkThinking(ctx, env, timeout),
		checkDatabase(ctx, env),
		checkPgpass(),
		checkBinary("chromium", locateChrome(), chromeFix),
		doctorCheck{mark: "✓", name: "clipboard", detail: clipBackend(os.Environ())},
		checkBinary("git", findGit(), "install git"),
		checkTerminal(),
	)
	return out
}

func checkConfigFile(env *Env) (doctorCheck, doctorCheck) {
	path := env.ConfigPath
	if path == "" {
		path = config.DefaultPath()
	}
	fi, err := os.Stat(path)
	if err != nil {
		missing := doctorCheck{mark: "✗", name: "config file", detail: "missing", fix: "konsolai /config init"}
		mode := doctorCheck{mark: "✗", name: "config mode", detail: "missing", fix: "chmod 0600 " + path}
		return missing, mode
	}
	file := doctorCheck{mark: "✓", name: "config file", detail: path}
	f, err := config.Load(path)
	if err != nil {
		file = doctorCheck{mark: "✗", name: "config file", detail: firstLine(err.Error()), fix: "konsolai /config validate"}
	} else {
		name := env.Profile.Name
		if name == "" {
			name = "default"
		}
		if _, err := f.Resolve(name); err != nil {
			file = doctorCheck{mark: "✗", name: "config file", detail: firstLine(err.Error()), fix: "konsolai /config validate"}
		}
	}
	perm := fi.Mode().Perm()
	mode := doctorCheck{mark: "✓", name: "config mode", detail: fmt.Sprintf("%04o", perm)}
	if perm != 0600 {
		mode = doctorCheck{mark: "✗", name: "config mode", detail: fmt.Sprintf("%04o", perm), fix: "chmod 0600 " + path}
	}
	return file, mode
}

func checkModels(ctx context.Context, name, endpoint, fix string, timeout time.Duration) doctorCheck {
	models, hit := probe(ctx, endpoint, "models", timeout)
	if hit.mark != "✓" {
		hit.name = name
		hit.fix = fix
		return hit
	}
	detail := "ok"
	if len(models) > 0 {
		detail = strings.Join(limitNames(models, 8), ", ")
	}
	return doctorCheck{mark: "✓", name: name, detail: detail}
}

func checkKind(ctx context.Context, name, endpoint, kind, fix string, timeout time.Duration) doctorCheck {
	_, hit := probe(ctx, endpoint, kind, timeout)
	hit.name = name
	if hit.mark != "✓" {
		hit.fix = fix
	}
	return hit
}

func checkThinking(ctx context.Context, env *Env, timeout time.Duration) doctorCheck {
	if !env.Profile.MainThinking() {
		return doctorCheck{mark: "✓", name: "thinking kwargs", detail: "off"}
	}
	code, err := probeThinking(ctx, env.Profile.MainEndpoint(), env.Profile.MainModelID(), timeout)
	if err != nil {
		return doctorCheck{mark: "!", name: "thinking kwargs", detail: probeErr(err), fix: "models." + env.Profile.Roles.Main + ".thinking_kwargs"}
	}
	if code >= 400 && code < 500 {
		return doctorCheck{mark: "!", name: "thinking kwargs", detail: fmt.Sprintf("http %d", code), fix: "models." + env.Profile.Roles.Main + ".thinking_kwargs"}
	}
	if code < 200 || code >= 300 {
		return doctorCheck{mark: "!", name: "thinking kwargs", detail: fmt.Sprintf("http %d", code), fix: "models." + env.Profile.Roles.Main + ".thinking_kwargs"}
	}
	return doctorCheck{mark: "✓", name: "thinking kwargs", detail: "accepted"}
}

func checkDatabase(ctx context.Context, env *Env) doctorCheck {
	if strings.TrimSpace(env.Profile.Database.URL) == "" {
		return doctorCheck{mark: "!", name: "database", detail: "not configured", fix: "database.url"}
	}
	var st *store.Store
	if env.Mem != nil {
		st = env.Mem.Store()
	}
	if st == nil {
		return doctorCheck{mark: "✗", name: "database", detail: "cannot connect", fix: "database.url"}
	}
	ver, err := st.SchemaVersion(ctx)
	if err != nil {
		return doctorCheck{mark: "✗", name: "database", detail: "schema unknown", fix: "database.url"}
	}
	latest, err := store.LatestVersion()
	if err != nil {
		return doctorCheck{mark: "✗", name: "database", detail: "schema unknown", fix: "database.url"}
	}
	vec, err := st.HasVector(ctx)
	mark := "✓"
	var fixes []string
	vecText := "present"
	if err != nil {
		mark = "✗"
		vecText = "unknown"
		fixes = append(fixes, "database.url")
	} else if !vec {
		mark = "✗"
		vecText = "missing"
		fixes = append(fixes, "CREATE EXTENSION vector")
	}
	detail := fmt.Sprintf("reachable, pgvector %s, schema %d", vecText, ver)
	if ver != latest {
		mark = "✗"
		detail = fmt.Sprintf("reachable, pgvector %s, schema %d want %d", vecText, ver, latest)
		fixes = append(fixes, "reopen konsolai so migrations run")
	}
	return doctorCheck{mark: mark, name: "database", detail: detail, fix: strings.Join(fixes, "; ")}
}

func checkPgpass() doctorCheck {
	home, err := doctorHome()
	if err != nil {
		return doctorCheck{mark: "!", name: "pgpass", detail: "home unknown", fix: "chmod 0600 ~/.pgpass"}
	}
	path := filepath.Join(home, ".pgpass")
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return doctorCheck{mark: "✓", name: "pgpass", detail: "absent"}
		}
		return doctorCheck{mark: "!", name: "pgpass", detail: "unreadable", fix: "chmod 0600 ~/.pgpass"}
	}
	perm := fi.Mode().Perm()
	if perm == 0600 {
		return doctorCheck{mark: "✓", name: "pgpass", detail: "0600"}
	}
	return doctorCheck{mark: "✗", name: "pgpass", detail: fmt.Sprintf("%04o", perm), fix: "chmod 0600 ~/.pgpass"}
}

func checkBinary(name, found, fix string) doctorCheck {
	if found == "" {
		return doctorCheck{mark: "✗", name: name, detail: "not found", fix: fix}
	}
	return doctorCheck{mark: "✓", name: name, detail: found}
}

func checkTerminal() doctorCheck {
	w, h := doctorTermSize()
	nc := "off"
	if os.Getenv("NO_COLOR") != "" {
		nc = "on"
	}
	detail := fmt.Sprintf("color=%s width=%d height=%d NO_COLOR=%s", colorName(), w, h, nc)
	return doctorCheck{mark: "✓", name: "terminal", detail: detail}
}

func locateChrome() string {
	if p := strings.TrimSpace(os.Getenv("KONSOLAI_CHROME")); p != "" {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return filepath.Base(p)
		}
		return ""
	}
	for _, name := range doctorChromeBins {
		if p, err := doctorLookPath(name); err == nil && p != "" {
			return name
		}
	}
	return ""
}

func findGit() string {
	if p, err := doctorLookPath("git"); err == nil && p != "" {
		return "git"
	}
	return ""
}

func probe(ctx context.Context, endpoint, kind string, timeout time.Duration) ([]string, doctorCheck) {
	if strings.TrimSpace(endpoint) == "" {
		return nil, doctorCheck{mark: "✗", detail: "unreachable"}
	}
	if timeout <= 0 {
		timeout = 300 * time.Millisecond
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := probeRequest(cctx, endpoint, kind)
	if err != nil {
		return nil, doctorCheck{mark: "✗", detail: "unreachable"}
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return nil, doctorCheck{mark: "✗", detail: probeErr(err)}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, doctorCheck{mark: "✗", detail: fmt.Sprintf("http %d", resp.StatusCode)}
	}
	if kind != "models" {
		return nil, doctorCheck{mark: "✓", detail: "ok"}
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &parsed)
	var ids []string
	for _, d := range parsed.Data {
		if d.ID != "" {
			ids = append(ids, d.ID)
		}
	}
	return ids, doctorCheck{mark: "✓", detail: "ok"}
}

func probeRequest(ctx context.Context, endpoint, kind string) (*http.Request, error) {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	switch kind {
	case "embed":
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/embed", bytes.NewReader([]byte(`{"inputs":["ok"]}`)))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
		return req, err
	case "rerank":
		return http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/info", nil)
	default:
		return http.NewRequestWithContext(ctx, http.MethodGet, modelsURL(endpoint), nil)
	}
}

func probeThinking(ctx context.Context, endpoint, model string, timeout time.Duration) (int, error) {
	if timeout <= 0 {
		timeout = 300 * time.Millisecond
	}
	if model == "" {
		model = "probe"
	}
	payload := []byte(`{"model":` + jsonString(model) + `,"messages":[{"role":"user","content":"ok"}],"max_tokens":1,"chat_template_kwargs":{"enable_thinking":false}}`)
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	raw := strings.TrimRight(strings.TrimSpace(endpoint), "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, raw, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	return resp.StatusCode, nil
}

func modelsURL(endpoint string) string {
	if strings.HasSuffix(endpoint, "/v1") {
		return endpoint + "/models"
	}
	return endpoint + "/v1/models"
}

func probeErr(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var ue *url.Error
	if errors.As(err, &ue) && ue.Timeout() {
		return "timeout"
	}
	return "unreachable"
}

func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

func limitNames(names []string, n int) []string {
	if len(names) <= n {
		return names
	}
	out := append([]string{}, names[:n]...)
	out = append(out, fmt.Sprintf("+%d", len(names)-n))
	return out
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func colorName() string {
	switch theme.Mode() {
	case theme.ColorTrue:
		return "truecolor"
	case theme.Color256:
		return "256"
	default:
		return "none"
	}
}

func defaultTermSize() (int, int) {
	w, h := 80, 24
	if n, ok := envInt("COLUMNS"); ok {
		w = n
	}
	if n, ok := envInt("LINES"); ok {
		h = n
	}
	tw, th, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return w, h
	}
	if _, ok := envInt("COLUMNS"); !ok && tw > 0 {
		w = tw
	}
	if _, ok := envInt("LINES"); !ok && th > 0 {
		h = th
	}
	return w, h
}

func envInt(key string) (int, bool) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return 0, false
	}
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 {
		return 0, false
	}
	return n, true
}

func writeDoctor(w io.Writer, c doctorCheck) {
	p := ui.Current()
	mark := c.mark
	switch c.mark {
	case "✓":
		mark = p.Success(c.mark)
	case "!":
		mark = p.Warn(c.mark)
	case "✗":
		mark = p.Error(c.mark)
	}
	fmt.Fprintf(w, "%s  %s  %s\n", mark, p.Muted(padCell(c.name, 16, false)), c.detail)
	if c.mark != "✓" && c.fix != "" {
		fmt.Fprintf(w, "   %s\n", p.Muted("fix: "+c.fix))
	}
}
