package commands

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/repo"
	"github.com/SniksaX/Konsolai/internal/turn"
	"github.com/SniksaX/Konsolai/internal/ui"
)

func runStatus(ctx context.Context, env *Env, _ []string) int {
	ver := env.Version
	if ver == "" {
		ver = "dev"
	}
	fmt.Fprintf(env.out(), "version        %s\n", ver)
	fmt.Fprintf(env.out(), "profile        %s\n", env.Profile.Name)
	ep := env.Profile.MainEndpoint()
	probe := modelsProbe(ctx, env.Profile, env.Profile.Roles.Main, ep)
	fmt.Fprintf(env.out(), "endpoint       %s  %s\n", endpointHost(ep, env.Profile.ModelRemote(env.Profile.Roles.Main)), probe.reach)
	model := env.Profile.MainModelID()
	configured := strings.TrimSpace(model)
	note := ""
	win := 0
	if env.Runner != nil && env.Runner.Client != nil {
		if id, err := env.Runner.Client.ResolveModel(ctx); err == nil && id != "" {
			if model == "" {
				model = id
			}
		} else if err != nil {
			if _, probeErr := llm.AsProbe(err); !probeErr {
				note = resolutionNote(model, err)
				model = dashIfEmpty(model)
			}
		}
		// The probe runs before the client resolves and may be stale. Only a
		// configured id is checked against it; an id the client resolved from
		// the server is already the server's answer, so noting the earlier
		// probe there would contradict a successful resolution.
		if configured != "" && probe.kind == llm.ProbeOK && !containsID(probe.ids, model) {
			note = "  (not served; server serves: " + strings.Join(limitNames(probe.ids, 8), ", ") + ")"
		}
		win = env.Runner.Client.ContextWindow()
	} else {
		resolved, resolvedNote := servedModel(env.Profile, probe, model)
		model, note = resolved, resolvedNote
	}
	if model == "" {
		model = "-"
	}
	fmt.Fprintf(env.out(), "model          %s%s\n", model, note)
	if win > 0 {
		fmt.Fprintf(env.out(), "context        %d\n", win)
	} else {
		fmt.Fprintln(env.out(), "context        unknown")
	}
	fastReach := ""
	if !env.Profile.FastOn() {
		fmt.Fprintf(env.out(), "fast_endpoint  %s\n", ui.Current().Muted("not configured"))
		fmt.Fprintf(env.out(), "fast_model     %s\n", ui.Current().Muted("not configured"))
	} else {
		fastEp := env.Profile.FastEndpoint()
		fastProbe := modelsProbe(ctx, env.Profile, env.Profile.Roles.Fast, fastEp)
		fastReach = fastProbe.reach
		fastModel := env.Profile.FastModelID()
		fastNote := ""
		if env.Runner != nil && env.Runner.FastClient != nil {
			if id, err := env.Runner.FastClient.ResolveModel(ctx); err == nil && id != "" {
				if fastModel == "" {
					fastModel = id
				}
			} else if err != nil {
				if _, probeErr := llm.AsProbe(err); !probeErr && fastModel == "" {
					fastModel, fastNote = "-", resolutionNote(fastModel, err)
				}
			}
		} else {
			fastModel, fastNote = servedModel(env.Profile, fastProbe, fastModel)
		}
		if fastModel == "" {
			fastModel = "-"
		}
		fmt.Fprintf(env.out(), "fast_endpoint  %s  %s\n", endpointHost(fastEp, env.Profile.ModelRemote(env.Profile.Roles.Fast)), fastReach)
		fmt.Fprintf(env.out(), "fast_model     %s%s\n", fastModel, fastNote)
	}
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
	pol := env.Profile.Agent.BuildThinking
	if pol == "" {
		pol = "plan_step"
	}
	fmt.Fprintf(env.out(), "build_thinking %s\n", pol)
	if env.Profile.EmbedURL() == "" {
		fmt.Fprintf(env.out(), "embed          %s\n", ui.Current().Muted("not configured"))
	} else {
		fmt.Fprintf(env.out(), "embed          %s  %s\n", turn.EndpointHost(env.Profile.EmbedURL()), httpReach(ctx, env.Profile.EmbedURL()))
	}
	writeEmbedDims(ctx, env)
	if env.Profile.RerankURL() == "" {
		fmt.Fprintf(env.out(), "rerank         %s\n", ui.Current().Muted("not configured"))
	} else {
		fmt.Fprintf(env.out(), "rerank         %s  %s\n", turn.EndpointHost(env.Profile.RerankURL()), httpReach(ctx, env.Profile.RerankURL()))
	}
	writeMCPStatus(ctx, env)
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

// endpointHost is the endpoint host plus the remote marker /set model appends
// to a remote model's host.
func endpointHost(endpoint string, remote bool) string {
	host := turn.EndpointHost(endpoint)
	if remote {
		host += "  remote"
	}
	return host
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

// modelProbe is one /models probe for the status lines.
type modelProbe struct {
	reach string
	ids   []string
	kind  llm.ProbeKind
}

// modelsProbe probes one role's endpoint with the key the turn would send.
// A local endpoint keeps the 3s status budget; a remote one reads the
// configured remote probe timeout.
func modelsProbe(ctx context.Context, prof config.Profile, role, endpoint string) modelProbe {
	if strings.TrimSpace(endpoint) == "" {
		return modelProbe{reach: "unreachable"}
	}
	c := llm.New(endpoint, "")
	c.SetAPIKey(prof.APIKeyResolver(roleModelName(prof, role)))
	c.SetProbeTimeout(3 * time.Second)
	c.SetRemoteProbeTimeout(prof.RemoteProbeTimeout())
	res := c.Probe(ctx)
	lat := res.Latency.Round(time.Millisecond)
	out := modelProbe{ids: res.IDs, kind: res.Kind}
	switch res.Kind {
	case llm.ProbeOK:
		out.reach = "ok " + lat.String()
	case llm.ProbeSlow:
		out.reach = "slow " + lat.String()
	case llm.ProbeAuthFailed:
		out.reach = "auth failed " + lat.String()
	case llm.ProbeHTTPError:
		out.reach = fmt.Sprintf("http %d %s", res.Status, lat)
	default:
		out.reach = "unreachable"
	}
	return out
}

// servedModel picks the id the status line may show without making one up:
// the configured id when the server serves it, the single served id when the
// configuration leaves it empty, and a clear note otherwise.
func servedModel(prof config.Profile, probe modelProbe, configured string) (string, string) {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		if probe.kind == llm.ProbeOK {
			switch len(probe.ids) {
			case 1:
				return probe.ids[0], ""
			case 0:
				return "-", ""
			default:
				return "-", "  (server serves: " + strings.Join(limitNames(probe.ids, 8), ", ") + "; set a model id)"
			}
		}
		return "-", ""
	}
	if probe.kind == llm.ProbeOK && !containsID(probe.ids, configured) {
		return configured, "  (not served; server serves: " + strings.Join(limitNames(probe.ids, 8), ", ") + ")"
	}
	return configured, ""
}

func resolutionNote(configured string, err error) string {
	msg := strings.TrimPrefix(err.Error(), "llm: ")
	return "  (" + dashIfEmpty(configured) + ": " + msg + ")"
}

func dashIfEmpty(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func roleModelName(prof config.Profile, role string) string {
	if role == "fast" {
		if name := strings.TrimSpace(prof.Roles.Fast); name != "" {
			return name
		}
		return "default"
	}
	if name := strings.TrimSpace(prof.Roles.Main); name != "" {
		return name
	}
	return "default"
}

func dbStatus(ctx context.Context, env *Env) string {
	if env.Profile.Database.URL == "" {
		return "disabled: not configured"
	}
	if env.Mem == nil || env.Mem.Store() == nil {
		return "disabled: cannot connect"
	}
	host, db := redactDB(env.Profile.Database.URL)
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
