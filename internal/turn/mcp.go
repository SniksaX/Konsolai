package turn

import (
	"context"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/mcp"
	"github.com/SniksaX/Konsolai/internal/redact"
	"github.com/SniksaX/Konsolai/internal/tools"
)

// MCPRuntime owns the process MCP manager and the tool bridge. One runtime is
// built per process and shared by the REPL, one-shot turns and /mcp status.
type MCPRuntime struct {
	Profile   config.Config
	Manager   *mcp.Manager
	Bridge    *tools.MCPBridge
	MaxInline int
}

// NewMCPRuntime maps the profile servers to manager specs and builds the lazy
// manager plus the tool bridge. A profile without servers yields a runtime with
// no manager.
func NewMCPRuntime(profile config.Config, projectDir, version string) (*MCPRuntime, error) {
	rt := &MCPRuntime{Profile: profile, MaxInline: profile.MCP.MaxInlineTools}
	if rt.MaxInline <= 0 {
		rt.MaxInline = 15
	}
	specs := make([]mcp.Spec, 0, len(profile.MCP.Servers))
	for _, name := range profile.MCPServerNames() {
		s, ok := profile.MCPServer(name)
		if !ok {
			continue
		}
		specs = append(specs, mcp.Spec{
			Name:          name,
			Transport:     s.Transport,
			Command:       s.Command,
			Args:          append([]string(nil), s.Args...),
			Env:           cloneStringMap(s.Env),
			URL:           s.URL,
			Headers:       cloneStringMap(s.Headers),
			Trust:         s.TrustMode(),
			Enabled:       s.Enabled,
			Private:       s.Private,
			Timeout:       time.Duration(s.Timeout()) * time.Millisecond,
			ReadOnlyTools: append([]string(nil), s.Tools.ReadOnly...),
		})
	}
	if len(specs) == 0 {
		return rt, nil
	}
	mgr, err := mcp.NewManager(specs, mcp.Options{ProjectDir: projectDir, ClientVersion: version})
	if err != nil {
		return nil, err
	}
	rt.Manager = mgr
	rt.Bridge = tools.NewMCPBridge(mgr, profile)
	return rt, nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Close releases every MCP session.
func (rt *MCPRuntime) Close() {
	if rt != nil && rt.Manager != nil {
		_ = rt.Manager.CloseAll()
	}
}

// ToolsFor returns the MCP tools one intent may see: none for answer, only the
// read-only ones for lookup, the whole allowed catalog for act/build. Above the
// inline cap it returns the single find meta-tool instead of every schema.
func (rt *MCPRuntime) ToolsFor(ctx context.Context, intent string) ([]tools.Tool, error) {
	if rt == nil || rt.Bridge == nil {
		return nil, nil
	}
	if intent == "answer" {
		return nil, nil
	}
	exposed, err := rt.Bridge.Exposed(ctx)
	if err != nil {
		return nil, err
	}
	filtered := exposed[:0]
	for _, e := range exposed {
		if intent == "lookup" && !e.ReadOnly {
			continue
		}
		filtered = append(filtered, e)
	}
	if len(filtered) == 0 {
		return nil, nil
	}
	if len(filtered) > rt.MaxInline {
		return []tools.Tool{rt.Bridge.FindToolFor(filtered)}, nil
	}
	out := make([]tools.Tool, 0, len(filtered))
	for _, e := range filtered {
		out = append(out, e.Tool)
	}
	return out, nil
}

// MCPStatus is one display-only live row for /mcp, /status and /doctor.
type MCPStatus struct {
	State      string
	Tools      int
	ToolsKnown bool
	ToolNames  []string
	Err        string
	MissingEnv []string
}

// Status probes one server for the status commands. It is the only place that
// connects a server merely to display it.
func (rt *MCPRuntime) Status(ctx context.Context, name string) MCPStatus {
	server, ok := rt.Profile.MCPServer(name)
	if !ok {
		return MCPStatus{State: "not connected"}
	}
	st := MCPStatus{MissingEnv: missingEnvRefs(server)}
	if !server.Enabled {
		st.State = "disabled"
		return st
	}
	if rt.Manager == nil {
		st.State = "not connected"
		return st
	}
	if rt.Manager.ServerDown(name) {
		st.State = "down"
		return st
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	list, err := rt.Manager.Tools(cctx, name)
	if err != nil {
		st.State = "down"
		st.Err = redact.String(err.Error())
		return st
	}
	st.State = "connected"
	st.ToolsKnown = true
	st.Tools = len(list)
	names := make([]string, 0, len(list))
	for _, t := range list {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	st.ToolNames = names
	return st
}

func missingEnvRefs(s config.MCPServer) []string {
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		for {
			i := strings.Index(v, "${")
			if i < 0 {
				return
			}
			rest := v[i+2:]
			j := strings.Index(rest, "}")
			if j < 0 {
				return
			}
			name := rest[:j]
			if name != "" && os.Getenv(name) == "" && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
			v = rest[j+1:]
		}
	}
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		add(s.Env[k])
	}
	hkeys := make([]string, 0, len(s.Headers))
	for k := range s.Headers {
		hkeys = append(hkeys, k)
	}
	sort.Strings(hkeys)
	for _, k := range hkeys {
		add(s.Headers[k])
	}
	sort.Strings(out)
	return out
}
