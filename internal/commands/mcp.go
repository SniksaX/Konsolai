package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// MCPServerStatus is one live row reported by an MCP manager.
type MCPServerStatus struct {
	State      string
	Tools      int
	ToolsKnown bool
}

// MCPManager reports the live state of the configured MCP servers. MCP is nil
// until a later phase wires a manager in, so /mcp list then says not connected.
type MCPManager interface {
	Status(name string) MCPServerStatus
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
