package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/siraj/konsolai/internal/repo"
	"github.com/siraj/konsolai/internal/turn"
)

func runStatus(ctx context.Context, env *Env, _ []string) int {
	ver := env.Version
	if ver == "" {
		ver = "dev"
	}
	fmt.Fprintf(env.out(), "version        %s\n", ver)
	fmt.Fprintf(env.out(), "profile        %s\n", env.Profile.Name)
	ep := env.Profile.Endpoint
	fmt.Fprintf(env.out(), "endpoint       %s  %s\n", turn.EndpointHost(ep), modelsProbe(ctx, ep))
	model := env.Profile.Model
	win := 0
	if env.Runner != nil && env.Runner.Client != nil {
		win = env.Runner.Client.ContextWindow()
	}
	fmt.Fprintf(env.out(), "model          %s\n", model)
	if win > 0 {
		fmt.Fprintf(env.out(), "context        %d\n", win)
	} else {
		fmt.Fprintln(env.out(), "context        unknown")
	}
	fmt.Fprintf(env.out(), "database       %s\n", dbStatus(ctx, env))
	cwd := env.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	fmt.Fprintf(env.out(), "cwd            %s\n", cwd)
	fmt.Fprintf(env.out(), "repo           %s\n", repo.Root(cwd))
	fmt.Fprintf(env.out(), "clipboard      %s\n", clipBackend(os.Environ()))
	pol := env.Profile.BuildThinking
	if pol == "" {
		pol = "plan_step"
	}
	fmt.Fprintf(env.out(), "build_thinking %s\n", pol)
	return 0
}

func modelsProbe(ctx context.Context, endpoint string) string {
	if endpoint == "" {
		return "unreachable"
	}
	u := strings.TrimRight(endpoint, "/")
	if !strings.HasSuffix(u, "/v1") && !strings.HasSuffix(u, "/models") {
		// endpoint is typically .../v1
	}
	reqURL := u + "/models"
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	t0 := time.Now()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "unreachable"
	}
	resp, err := http.DefaultClient.Do(req)
	d := time.Since(t0).Round(time.Millisecond)
	if err != nil {
		return "unreachable"
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Sprintf("error %d %s", resp.StatusCode, d)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return "ok " + d.String()
}

func dbStatus(ctx context.Context, env *Env) string {
	if env.Profile.DatabaseURL == "" {
		return "disabled: not configured"
	}
	if env.Mem == nil || env.Mem.Store() == nil {
		return "disabled: cannot connect"
	}
	host, db := redactDB(env.Profile.DatabaseURL)
	ver, err := env.Mem.Store().SchemaVersion(ctx)
	if err != nil {
		return host + "/" + db + " (schema unknown)"
	}
	return fmt.Sprintf("%s/%s schema %d", host, db, ver)
}

func redactDB(raw string) (host, db string) {
	u, err := url.Parse(raw)
	if err != nil {
		return "db", ""
	}
	host = u.Hostname()
	if p := u.Port(); p != "" {
		host += ":" + p
	}
	db = strings.TrimPrefix(u.Path, "/")
	if i := strings.IndexByte(db, '?'); i >= 0 {
		db = db[:i]
	}
	return host, db
}

func clipBackend(env []string) string {
	get := func(k string) string {
		for _, e := range env {
			if key, val, ok := strings.Cut(e, "="); ok && key == k {
				return val
			}
		}
		return ""
	}
	if get("WAYLAND_DISPLAY") != "" {
		return "wl-copy"
	}
	if get("DISPLAY") != "" {
		return "xclip"
	}
	return "osc52"
}
