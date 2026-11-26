package commands

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/SniksaX/Konsolai/internal/ui"
)

// MCPServerStatus is one live row reported by an MCP manager. Every field is
// display-only: an env value, a header value and a command line never enter it.
type MCPServerStatus struct {
	// State is connected, down, not connected, or disabled.
	State      string
	Tools      int
	ToolsKnown bool
	// ToolNames lists the server's tool names once the list is known; the UI
	// shows each one as "server: tool".
	ToolNames []string
	// Err is one short line from the last failure, already redacted.
	Err string
	// MissingEnv names unresolved ${NAME} references. It never carries a value.
	MissingEnv []string
}

// MCPManager reports the live state of the configured MCP servers. The field is
// nil until the runtime is wired, so /mcp list then says not connected.
type MCPManager interface {
	Status(name string) MCPServerStatus
	// Known reports the state without touching the server: connected, down,
	// not connected, or disabled. It must never connect, because the welcome
	// counts it on every repaint.
	Known(name string) string
}

func runMCP(ctx context.Context, env *Env, args []string) int {
	_ = ctx
	if env == nil {
		return 1
	}
	if len(args) == 0 {
		return mcpList(env)
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "list":
		if len(args) > 1 {
			fmt.Fprintln(env.errw(), "usage: /mcp [list]")
			return 2
		}
		return mcpList(env)
	default:
		fmt.Fprintln(env.errw(), "usage: /mcp [list]")
		return 2
	}
}

// mcpList prints one row per configured server. It never prints an env value,
// a header value or a command line.
func mcpList(env *Env) int {
	names := env.Profile.MCPServerNames()
	if len(names) == 0 {
		fmt.Fprintln(env.out(), "no MCP servers configured")
		return 0
	}
	fmt.Fprintf(env.out(), "%-16s %-9s %-9s %-10s %-8s %-6s %s\n",
		"name", "transport", "state", "trust", "private", "tools", "status")
	for _, name := range names {
		server, ok := env.Profile.MCPServer(name)
		if !ok {
			continue
		}
		state := "disabled"
		if server.Enabled {
			state = "enabled"
		}
		private := "-"
		if server.Private {
			private = "private"
		}
		tools := "-"
		status := "not connected"
		if env.MCP != nil {
			live := env.MCP.Status(name)
			if live.ToolsKnown {
				tools = strconv.Itoa(live.Tools)
			}
			if strings.TrimSpace(live.State) != "" {
				status = strings.TrimSpace(live.State)
			}
		}
		fmt.Fprintf(env.out(), "%-16s %-9s %-9s %-10s %-8s %-6s %s\n",
			name, server.Transport, state, server.TrustMode(), private, tools, status)
	}
	return 0
}

func mcpTree(*Env) []Choice {
	return []Choice{
		{Value: "list", Description: "List configured MCP servers", Terminal: true},
	}
}

// mcpRow is the display view of one configured MCP server. Only profile fields
// and the manager's display-only status enter it: a command line, an env value
// and a header value never do.
type mcpRow struct {
	Name       string
	State      string
	Transport  string
	Trust      string
	Private    bool
	Tools      int
	ToolsKnown bool
	ToolNames  []string
	Err        string
	MissingEnv []string
}

// mcpRows reads one row per configured server and probes the live state once
// per server. /status and /doctor call it; the welcome uses the manager's Known
// instead so a repaint never connects anything.
func mcpRows(ctx context.Context, env *Env) []mcpRow {
	if env == nil {
		return nil
	}
	names := env.Profile.MCPServerNames()
	if len(names) == 0 {
		return nil
	}
	out := make([]mcpRow, 0, len(names))
	for _, name := range names {
		server, ok := env.Profile.MCPServer(name)
		if !ok {
			continue
		}
		row := mcpRow{
			Name:      name,
			Transport: strings.ToLower(strings.TrimSpace(server.Transport)),
			Trust:     server.TrustMode(),
			Private:   server.Private,
		}
		if env.MCP != nil {
			live := env.MCP.Status(name)
			row.State = strings.TrimSpace(live.State)
			row.Tools = live.Tools
			row.ToolsKnown = live.ToolsKnown
			row.ToolNames = append([]string(nil), live.ToolNames...)
			sort.Strings(row.ToolNames)
			row.Err = firstLine(live.Err)
			row.MissingEnv = append([]string(nil), live.MissingEnv...)
		}
		if !server.Enabled {
			row.State = "disabled"
		}
		if row.State == "" {
			row.State = "not connected"
		}
		out = append(out, row)
	}
	return out
}

const (
	// mcpToolLinesShown caps the per-server tool lines in /status and /doctor.
	mcpToolLinesShown = 8
	mcpStatusLabel    = "mcp"
	mcpToolLabel      = "mcp tool"
	mcpStatusWidth    = 14
)

// mcpDetail is one server's line: state, transport, trust, tool count, a
// private marker, the unresolved env names and one short error line. Values
// never appear, only names.
func mcpDetail(r mcpRow) string {
	parts := []string{r.State, r.Transport, "trust " + r.Trust}
	if r.ToolsKnown {
		parts = append(parts, fmt.Sprintf("%d tools", r.Tools))
	}
	if r.Private {
		parts = append(parts, "private")
	}
	if len(r.MissingEnv) > 0 {
		parts = append(parts, "missing "+strings.Join(r.MissingEnv, ", "))
	}
	if r.Err != "" {
		parts = append(parts, "error: "+r.Err)
	}
	return strings.Join(parts, "  ")
}

// mcpShownTools returns the tool names to print and how many were left out.
func mcpShownTools(r mcpRow) ([]string, int) {
	if len(r.ToolNames) <= mcpToolLinesShown {
		return r.ToolNames, 0
	}
	return r.ToolNames[:mcpToolLinesShown], len(r.ToolNames) - mcpToolLinesShown
}

// writeMCPStatus prints the /status MCP rows after the model and RAG lines. A
// profile without servers prints one "not configured" line, and a known tool
// list is printed as "server: tool".
func writeMCPStatus(ctx context.Context, env *Env) {
	if env == nil {
		return
	}
	rows := mcpRows(ctx, env)
	if len(rows) == 0 {
		fmt.Fprintf(env.out(), "%-*s %s\n", mcpStatusWidth, mcpStatusLabel, ui.Current().Muted("not configured"))
		return
	}
	for _, r := range rows {
		fmt.Fprintf(env.out(), "%-*s %s  %s\n", mcpStatusWidth, mcpStatusLabel, r.Name, mcpDetail(r))
		shown, more := mcpShownTools(r)
		for _, tool := range shown {
			fmt.Fprintf(env.out(), "%-*s %s: %s\n", mcpStatusWidth, mcpToolLabel, r.Name, tool)
		}
		if more > 0 {
			fmt.Fprintf(env.out(), "%-*s %s: +%d more tools\n", mcpStatusWidth, mcpToolLabel, r.Name, more)
		}
	}
}

// mcpChecks reports one line per configured MCP server after the remote-model
// lines, then one line per known tool (capped). A warning is a "!" with the fix
// it needs; an unresolved env name is listed without its value.
func mcpChecks(ctx context.Context, env *Env) []doctorCheck {
	rows := mcpRows(ctx, env)
	var out []doctorCheck
	for _, r := range rows {
		check := doctorCheck{mark: "✓", name: mcpStatusLabel + " " + r.Name, detail: mcpDetail(r)}
		var fixes []string
		if r.State == "down" {
			check.mark = "!"
			fixes = append(fixes, "check the server command or URL, then /mcp")
		}
		if len(r.MissingEnv) > 0 {
			check.mark = "!"
			fixes = append(fixes, "export "+strings.Join(r.MissingEnv, ", "))
		}
		check.fix = strings.Join(fixes, "; ")
		out = append(out, check)
		shown, more := mcpShownTools(r)
		for _, tool := range shown {
			out = append(out, doctorCheck{mark: "✓", name: mcpToolLabel, detail: r.Name + ": " + tool})
		}
		if more > 0 {
			out = append(out, doctorCheck{mark: "✓", name: mcpToolLabel, detail: fmt.Sprintf("%s: +%d more tools", r.Name, more)})
		}
	}
	return out
}
