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
	reach, probeModel := modelsProbe(ctx, ep)
	fmt.Fprintf(env.out(), "endpoint       %s  %s\n", turn.EndpointHost(ep), reach)
	model := env.Profile.Model
	win := 0
	if env.Runner != nil && env.Runner.Client != nil {
		if id, err := env.Runner.Client.ResolveModel(ctx); err == nil {
			if model == "" {
				model = id
			}
		}
		win = env.Runner.Client.ContextWindow()
	}
	if model == "" {
		model = probeModel
	}
	if model == "" {
		model = "-"
	}
	fmt.Fprintf(env.out(), "model          %s\n", model)
	if win > 0 {
		fmt.Fprintf(env.out(), "context        %d\n", win)
	} else {
		fmt.Fprintln(env.out(), "context        unknown")
	}
	fastEp := env.Profile.FastEndpoint
	fastReach, fastModel := modelsProbe(ctx, fastEp)
	if env.Runner != nil && env.Runner.FastClient != nil {
		if id, err := env.Runner.FastClient.ResolveModel(ctx); err == nil && id != "" {
			fastModel = id
		}
	}
	if env.Profile.FastModel != "" && fastModel == "" {
		fastModel = env.Profile.FastModel
	}
	if fastModel == "" {
		fastModel = "-"
	}
	fmt.Fprintf(env.out(), "fast_endpoint  %s  %s\n", turn.EndpointHost(fastEp), fastReach)
	fmt.Fprintf(env.out(), "fast_model     %s\n", fastModel)
	routing := "main"
	if env.Runner != nil && env.Runner.RouterSource != "" {
		routing = env.Runner.RouterSource
	} else if strings.HasPrefix(fastReach, "ok") {
		routing = "fast"
	}
	fmt.Fprintf(env.out(), "routing        %s\n", routing)
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
	fmt.Fprintf(env.out(), "embed          %s  %s\n", turn.EndpointHost(env.Profile.EmbedEndpoint), httpReach(ctx, env.Profile.EmbedEndpoint))
	fmt.Fprintf(env.out(), "rerank         %s  %s\n", turn.EndpointHost(env.Profile.RerankEndpoint), httpReach(ctx, env.Profile.RerankEndpoint))
	if env.Mem != nil && env.Mem.Store() != nil && env.Mem.Store().RAGAvailable(ctx) {
		pid := env.ProjectID
		counts, err := env.Mem.Store().ChunkCounts(ctx, env.Profile.Name, pid)
		if err == nil {
			fmt.Fprintf(env.out(), "chunks         turn=%d summary=%d web=%d code=%d\n",
				counts["turn"], counts["summary"], counts["web"], counts["code"])
		}
		indexed := "no"
		if pid != nil {
			if _, has, err := env.Mem.Store().LastIndexedAt(ctx, *pid); err == nil && has {
				indexed = "yes"
			}
		}
		fmt.Fprintf(env.out(), "project index  %s\n", indexed)
	} else if env.Profile.RAGEnabled() {
		fmt.Fprintln(env.out(), "rag            unavailable")
	}
	return 0
}

func httpReach(ctx context.Context, endpoint string) string {
	if endpoint == "" {
		return "unreachable"
	}
	url := strings.TrimRight(endpoint, "/") + "/info"
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	t0 := time.Now()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
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
	return "ok " + d.String()
}

func modelsProbe(ctx context.Context, endpoint string) (reach, model string) {
	if endpoint == "" {
		return "unreachable", ""
	}
	u := strings.TrimRight(endpoint, "/")
	reqURL := u + "/models"
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	t0 := time.Now()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "unreachable", ""
	}
	resp, err := http.DefaultClient.Do(req)
	d := time.Since(t0).Round(time.Millisecond)
	if err != nil {
		return "unreachable", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Sprintf("error %d %s", resp.StatusCode, d), ""
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Data) > 0 {
		model = body.Data[0].ID
	}
	return "ok " + d.String(), model
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
