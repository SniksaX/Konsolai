package turn

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
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

	// profileMu guards the live profile snapshot. The REPL can change MCP
	// policy mid-session through /set mcp, so every turn re-reads it instead of
	// filtering with the profile captured at process start.
	profileMu sync.RWMutex

	privateMu    sync.Mutex
	privateAsked map[string]bool
	privateOK    map[string]bool
}

// mainRemote reports whether the main model's endpoint is remote.
func (rt *MCPRuntime) mainRemote() bool {
	if rt == nil {
		return false
	}
	rt.profileMu.RLock()
	remote := rt.Profile.Main().Remote()
	rt.profileMu.RUnlock()
	return remote
}

// privateAllowed applies the private-server rule for a remote main model: one
// confirmation per process, remembered for both allow and refuse. A local main
// model needs none. --yes skips the prompt.
func (rt *MCPRuntime) privateAllowed(server string, remoteMain, yes bool, confirm func(string) (bool, error)) bool {
	if !remoteMain {
		return true
	}
	rt.privateMu.Lock()
	if rt.privateAsked == nil {
		rt.privateAsked = map[string]bool{}
		rt.privateOK = map[string]bool{}
	}
	if rt.privateOK[server] {
		rt.privateMu.Unlock()
		return true
	}
	if rt.privateAsked[server] {
		rt.privateMu.Unlock()
		return false
	}
	rt.privateMu.Unlock()
	ok := false
	if yes {
		ok = true
	} else if confirm != nil {
		allow, err := confirm(server)
		ok = err == nil && allow
	}
	rt.privateMu.Lock()
	rt.privateAsked[server] = true
	if ok {
		rt.privateOK[server] = true
	}
	rt.privateMu.Unlock()
	return ok
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

// UseProfile refreshes the live profile snapshot used by the next turn. The
// process MCP manager keeps its sessions and its cached tool lists; only the
// trust, allow/deny, enabled, private and cap rules are re-read, so a trust
// change applies to the next request instead of a stale process catalog.
func (rt *MCPRuntime) UseProfile(profile config.Config) {
	if rt == nil {
		return
	}
	cap := profile.MCP.MaxInlineTools
	if cap <= 0 {
		cap = 15
	}
	rt.profileMu.Lock()
	defer rt.profileMu.Unlock()
	rt.Profile = profile
	rt.MaxInline = cap
	switch {
	case rt.Manager != nil:
		rt.Bridge = tools.NewMCPBridge(rt.Manager, profile)
	case rt.Bridge != nil:
		rt.Bridge = rt.Bridge.WithProfile(profile)
	}
}

// profileSnapshot returns the live profile, bridge and inline cap together.
func (rt *MCPRuntime) profileSnapshot() (config.Config, *tools.MCPBridge, int) {
	rt.profileMu.RLock()
	defer rt.profileMu.RUnlock()
	return rt.Profile, rt.Bridge, rt.MaxInline
}

// PrivateConfirmed reports whether a private server was already confirmed for
// a remote main model in this process. It never prompts.
func (rt *MCPRuntime) PrivateConfirmed(server string) bool {
	if rt == nil {
		return false
	}
	rt.privateMu.Lock()
	defer rt.privateMu.Unlock()
	return rt.privateOK[server]
}

// CachedToolCount reports the tool count already known for one server without
// connecting it, so the status displays stay lazy.
func (rt *MCPRuntime) CachedToolCount(server string) (int, bool) {
	if rt == nil || rt.Manager == nil {
		return 0, false
	}
	list, ok := rt.Manager.CachedTools(server)
	if !ok {
		return 0, false
	}
	return len(list), true
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
func (rt *MCPRuntime) ToolsFor(ctx context.Context, intent string, remoteMain, yes bool, confirm func(string) (bool, error)) ([]tools.Tool, error) {
	if rt == nil {
		return nil, nil
	}
	profile, bridge, maxInline := rt.profileSnapshot()
	if bridge == nil {
		return nil, nil
	}
	if intent == "answer" {
		return nil, nil
	}
	if remoteMain {
		// A private server must be confirmed before it is even asked for its
		// tool list: nothing is sent to it before the answer.
		filtered := profile
		filtered.MCP.Servers = map[string]config.MCPServer{}
		for name, server := range profile.MCP.Servers {
			filtered.MCP.Servers[name] = server
		}
		for name, server := range filtered.MCP.Servers {
			if server.Private && !rt.privateAllowed(name, remoteMain, yes, confirm) {
				delete(filtered.MCP.Servers, name)
			}
		}
		bridge = bridge.WithProfile(filtered)
	}
	exposed, err := bridge.Exposed(ctx)
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
	if len(filtered) > maxInline {
		return []tools.Tool{bridge.FindToolFor(filtered)}, nil
	}
	out := make([]tools.Tool, 0, len(filtered))
	for _, e := range filtered {
		out = append(out, e.Tool)
	}
	return out, nil
}

// Known reports a server state without connecting or listing tools. It is the
// cheap accessor the welcome uses on every repaint.
func (rt *MCPRuntime) Known(name string) string {
	if rt == nil {
		return "not connected"
	}
	profile, _, _ := rt.profileSnapshot()
	if server, ok := profile.MCPServer(name); ok && !server.Enabled {
		return "disabled"
	}
	if rt.Manager == nil {
		return "not connected"
	}
	return rt.Manager.Known(name)
}

// mcpRouterLines renders one safe line per configured MCP server for the
// intent router: name and policy metadata only, never schemas, commands,
// arguments, env values, headers or URLs.
func mcpRouterLines(profile config.Config) []string {
	names := profile.MCPServerNames()
	lines := make([]string, 0, len(names))
	for _, name := range names {
		server, ok := profile.MCPServer(name)
		if !ok {
			continue
		}
		state := "enabled"
		if !server.Enabled {
			state = "disabled"
		}
		line := fmt.Sprintf("%s: %s, trust %s, %s", name, server.Transport, server.TrustMode(), state)
		if server.Private {
			line += ", private"
		}
		if n := len(server.Tools.Allow); n > 0 {
			line += fmt.Sprintf(", %d allow globs", n)
		}
		if n := len(server.Tools.Deny); n > 0 {
			line += fmt.Sprintf(", %d deny globs", n)
		}
		lines = append(lines, line)
	}
	return lines
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
	profile, _, _ := rt.profileSnapshot()
	server, ok := profile.MCPServer(name)
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
