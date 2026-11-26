package commands

import (
	"context"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/mcp"
	"github.com/SniksaX/Konsolai/internal/redact"
	"github.com/SniksaX/Konsolai/internal/tools"
)

// MCPTool is one tool row a manager reports for /mcp tools. It is
// display-only: no env value, header value or command line enters it.
type MCPTool struct {
	Name        string
	Description string
	// ReadOnly is the server's own readOnlyHint, trusted only under the
	// read_only and auto trust levels.
	ReadOnly bool
}

// MCPProbe is one connect-and-list result for /mcp test.
type MCPProbe struct {
	Latency time.Duration
	Tools   int
}

// MCPToolLister is the optional manager capability /mcp tools reads. A
// manager that does not implement it still serves /mcp list.
type MCPToolLister interface {
	Tools(ctx context.Context, name string) ([]MCPTool, error)
}

// MCPProber is the optional manager capability /mcp test reads.
type MCPProber interface {
	Probe(ctx context.Context, name string) (MCPProbe, error)
}

const (
	mcpUsage          = "usage: /mcp [list | tools <name> | test <name>]"
	mcpToolDescCap    = 80
	mcpToolNameCap    = 40
	mcpToolTableFmt   = "  %-24s %-9s %-8s %-26s %-24s %s\n"
	mcpToolTableHead  = "  %-24s %-9s %-8s %-26s %-24s %s\n"
	mcpToolStateAllow = "allowed"
	mcpToolStateHide  = "hidden"
)

var mcpEnvRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// mcpToolRow is one rendered /mcp tools line.
type mcpToolRow struct {
	Name        string
	Kind        string
	State       string
	Offered     string
	Reason      string
	Description string
}

// mcpOfferContext is the caller-level state the offer rule needs beyond the
// per-tool trust and glob rules: a private server hidden under a remote main
// model, and whether the inline cap turns the catalog into mcp_find_tool only.
type mcpOfferContext struct {
	serverHidden bool
	hiddenReason string
	findOnly     bool
}

// mcpTools prints one row per tool: name, read-only or mutating under the
// server's trust and globs, whether it is allowed or hidden, the reason, and
// the first line of the description. It never prints a value.
func mcpTools(ctx context.Context, env *Env, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(env.errw(), "usage: /mcp tools <name>")
		return 2
	}
	name := strings.TrimSpace(args[0])
	server, ok := env.Profile.MCPServer(name)
	if !ok {
		fmt.Fprintf(env.errw(), "mcp: no MCP server %q\n", name)
		return 2
	}
	if !server.Enabled {
		fmt.Fprintf(env.errw(), "mcp: server %q is disabled\n", name)
		return 1
	}
	if env.MCP == nil {
		fmt.Fprintln(env.errw(), "mcp: no MCP manager is connected")
		return 1
	}
	lister, ok := env.MCP.(MCPToolLister)
	if !ok {
		fmt.Fprintln(env.errw(), "mcp: the MCP manager cannot list tools")
		return 1
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(server.Timeout())*time.Millisecond)
	defer cancel()
	list, err := lister.Tools(cctx, name)
	if err != nil {
		mcpInspectError(env, "tools", name, server, err)
		return 1
	}
	offer := mcpOfferForEnv(env, name, server, list)
	rows := mcpToolRows(server, name, list, offer)
	allowed, offered := 0, 0
	for _, row := range rows {
		if row.State == mcpToolStateAllow {
			allowed++
		}
		if row.Offered == mcpToolOfferedYes {
			offered++
		}
	}
	fmt.Fprintf(env.out(), "mcp %s: %s, trust %s, %d tools (%d allowed, %d hidden, %d offered now)\n",
		name, server.Transport, server.TrustMode(), len(rows), allowed, len(rows)-allowed, offered)
	if len(rows) == 0 {
		fmt.Fprintln(env.out(), "no tools")
		return 0
	}
	fmt.Fprintf(env.out(), mcpToolTableHead, "tool", "kind", "state", "offered", "reason", "description")
	for _, row := range rows {
		fmt.Fprintf(env.out(), mcpToolTableFmt, row.Name, row.Kind, row.State, row.Offered, row.Reason, row.Description)
	}
	return 0
}

const (
	mcpToolOfferedYes = "yes"
	mcpToolOfferedNo  = "no: "
)

// mcpOfferForEnv builds the caller-level offer context for one server. The
// private rule mirrors the turn: a remote main model needs a confirmation
// (remembered by the runtime) or --yes before a private server is offered; the
// inline cap mirrors the runtime default when the profile leaves it at zero.
func mcpOfferForEnv(env *Env, name string, server config.MCPServer, list []MCPTool) mcpOfferContext {
	if env == nil {
		return mcpOfferContext{}
	}
	offer := mcpOfferContext{}
	if env.Profile.Main().Remote() && server.Private && !env.Yes {
		confirmed := env.Runner != nil && env.Runner.MCPRuntime != nil && env.Runner.MCPRuntime.PrivateConfirmed(name)
		if !confirmed {
			offer.serverHidden = true
			offer.hiddenReason = "private (remote main)"
		}
	}
	cap := env.Profile.MCPInlineCap()
	if cap <= 0 {
		cap = 15
	}
	allowed := 0
	for _, tool := range list {
		candidate := mcp.Tool{Name: tool.Name, ReadOnly: tool.ReadOnly}
		if tools.MCPToolAllowed(server, name, candidate) {
			allowed++
		}
	}
	offer.findOnly = allowed > cap
	return offer
}

// mcpTest connects to one server once, lists its tools and reports the
// connect-and-list latency and tool count. Errors name an unresolved variable
// but never a value.
func mcpTest(ctx context.Context, env *Env, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(env.errw(), "usage: /mcp test <name>")
		return 2
	}
	name := strings.TrimSpace(args[0])
	server, ok := env.Profile.MCPServer(name)
	if !ok {
		fmt.Fprintf(env.errw(), "mcp: no MCP server %q\n", name)
		return 2
	}
	if !server.Enabled {
		fmt.Fprintf(env.errw(), "mcp: server %q is disabled\n", name)
		return 1
	}
	if env.MCP == nil {
		fmt.Fprintln(env.errw(), "mcp: no MCP manager is connected")
		return 1
	}
	prober, ok := env.MCP.(MCPProber)
	if !ok {
		fmt.Fprintln(env.errw(), "mcp: the MCP manager cannot test a server")
		return 1
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(server.Timeout())*time.Millisecond)
	defer cancel()
	probe, err := prober.Probe(cctx, name)
	if err != nil {
		mcpInspectError(env, "test", name, server, err)
		return 1
	}
	fmt.Fprintf(env.out(), "mcp %s: connected in %s, %d tools\n", name, mcpLatency(probe.Latency), probe.Tools)
	return 0
}

// mcpInspectError prints a short failure for /mcp tools and /mcp test. The
// manager error is redacted, and unresolved references are listed by name.
func mcpInspectError(env *Env, verb, name string, server config.MCPServer, err error) {
	msg := strings.TrimSpace(redact.String(err.Error()))
	if msg == "" {
		msg = "the server is not connected"
	}
	fmt.Fprintf(env.errw(), "mcp %s %s: %s\n", verb, name, msg)
	if missing := mcpMissingEnvRefs(server); len(missing) > 0 {
		fmt.Fprintf(env.errw(), "missing environment variable(s): %s\n", strings.Join(missing, ", "))
	}
}

// mcpMissingEnvRefs names unresolved ${ENV_VAR} references; only names, never
// values.
func mcpMissingEnvRefs(server config.MCPServer) []string {
	seen := map[string]bool{}
	var out []string
	collect := func(value string) {
		for _, match := range mcpEnvRef.FindAllStringSubmatch(value, -1) {
			name := match[1]
			if name == "" || seen[name] || strings.TrimSpace(os.Getenv(name)) != "" {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, key := range sortedKeys(server.Env) {
		collect(server.Env[key])
	}
	for _, key := range sortedKeys(server.Headers) {
		collect(server.Headers[key])
	}
	sort.Strings(out)
	return out
}

// mcpToolRows classifies every tool under the server's trust and globs, then
// marks what the model would be offered right now. The offer rule is the same
// one the turn uses: enabled, allow/deny, trust and the caller-level private
// and inline-cap decisions.
func mcpToolRows(server config.MCPServer, serverName string, list []MCPTool, offer mcpOfferContext) []mcpToolRow {
	rows := make([]mcpToolRow, 0, len(list))
	for _, tool := range list {
		kind, readOnly := mcpToolKind(server, tool)
		allowed, reason := mcpToolShown(server, serverName, tool.Name, readOnly)
		state := mcpToolStateAllow
		if !allowed {
			state = mcpToolStateHide
		}
		candidate := mcp.Tool{Name: tool.Name, ReadOnly: tool.ReadOnly}
		ok, why := tools.MCPToolOffered(server, serverName, candidate, offer.serverHidden, offer.hiddenReason)
		offered := mcpToolOfferedYes
		if !ok {
			offered = mcpToolOfferedNo + why
		} else if offer.findOnly {
			offered = mcpToolOfferedNo + "find tool only (inline cap)"
		}
		rows = append(rows, mcpToolRow{
			Name:        capMCPText(redact.String(tool.Name), mcpToolNameCap),
			Kind:        kind,
			State:       state,
			Offered:     offered,
			Reason:      reason,
			Description: mcpToolDescription(tool.Description),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

// mcpToolKind is read-only when a tools.read_only glob matches, or when the
// server's hint is trusted (read_only and auto trust); every other tool is
// mutating.
func mcpToolKind(server config.MCPServer, tool MCPTool) (string, bool) {
	for _, glob := range server.Tools.ReadOnly {
		if mcpGlobMatch(glob, tool.Name) {
			return "read-only", true
		}
	}
	if tool.ReadOnly {
		switch server.TrustMode() {
		case config.MCPTrustReadOnly, config.MCPTrustAuto:
			return "read-only", true
		}
	}
	return "mutating", false
}

// mcpToolShown applies allow/deny globs and the read_only trust filter. The
// reason names the rule that kept the tool out.
func mcpToolShown(server config.MCPServer, serverName, toolName string, readOnly bool) (bool, string) {
	if len(server.Tools.Allow) > 0 {
		if matched, _ := mcpAnyGlob(server.Tools.Allow, serverName, toolName); !matched {
			return false, "not in allow"
		}
	}
	if matched, glob := mcpAnyGlob(server.Tools.Deny, serverName, toolName); matched {
		return false, "deny " + glob
	}
	if server.TrustMode() == config.MCPTrustReadOnly && !readOnly {
		return false, "trust read_only"
	}
	if readOnly {
		for _, glob := range server.Tools.ReadOnly {
			if mcpGlobMatch(glob, toolName) {
				return true, "read_only glob"
			}
		}
		return true, "read-only hint (" + server.TrustMode() + ")"
	}
	return true, "mutating"
}

// mcpAnyGlob reports whether any glob matches the tool name or server:tool.
func mcpAnyGlob(globs []string, serverName, toolName string) (bool, string) {
	for _, glob := range globs {
		if mcpGlobMatch(glob, toolName) || mcpGlobMatch(glob, serverName+":"+toolName) {
			return true, glob
		}
	}
	return false, ""
}

func mcpGlobMatch(pattern, toolName string) bool {
	ok, err := path.Match(pattern, toolName)
	return err == nil && ok
}

// mcpToolDescription keeps the first line, redacts secret-shaped text and caps
// the result.
func mcpToolDescription(raw string) string {
	line := raw
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(redact.String(line))
	if line == "" {
		return "-"
	}
	return capMCPText(line, mcpToolDescCap)
}

// capMCPText caps text at n runes and marks a cut with an ellipsis.
func capMCPText(text string, n int) string {
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	runes := []rune(text)
	return string(runes[:n]) + "…"
}

// mcpLatency renders one /mcp test latency without a fake zero.
func mcpLatency(d time.Duration) string {
	if d < time.Millisecond {
		return "<1ms"
	}
	return d.Round(time.Millisecond).String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
