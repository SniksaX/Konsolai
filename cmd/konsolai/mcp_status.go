package main

import (
	"context"
	"errors"
	"time"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/turn"
)

// mcpStatusAdapter maps the turn-owned MCP runtime to the commands status
// contract. It is display-only: no value, command line or header ever leaves
// the manager unredacted here.
type mcpStatusAdapter struct {
	rt *turn.MCPRuntime
}

func (a mcpStatusAdapter) Known(name string) string {
	if a.rt == nil {
		return "not connected"
	}
	return a.rt.Known(name)
}

// Tools lists one server's tool catalog for /mcp tools. The command redacts
// the description and never prints a value.
func (a mcpStatusAdapter) Tools(ctx context.Context, name string) ([]commands.MCPTool, error) {
	if a.rt == nil || a.rt.Manager == nil {
		return nil, errors.New("mcp: no manager")
	}
	list, err := a.rt.Manager.Tools(ctx, name)
	if err != nil {
		return nil, err
	}
	out := make([]commands.MCPTool, 0, len(list))
	for _, tool := range list {
		out = append(out, commands.MCPTool{
			Name:        tool.Name,
			Description: tool.Description,
			ReadOnly:    tool.ReadOnly,
		})
	}
	return out, nil
}

// Probe times one connect-and-list for /mcp test.
func (a mcpStatusAdapter) Probe(ctx context.Context, name string) (commands.MCPProbe, error) {
	if a.rt == nil {
		return commands.MCPProbe{}, errors.New("mcp: no manager")
	}
	start := time.Now()
	status := a.rt.Status(ctx, name)
	probe := commands.MCPProbe{Latency: time.Since(start), Tools: status.Tools}
	if status.State != "connected" {
		msg := status.Err
		if msg == "" {
			msg = status.State
		}
		return probe, errors.New(msg)
	}
	return probe, nil
}

func (a mcpStatusAdapter) Status(name string) commands.MCPServerStatus {
	if a.rt == nil {
		return commands.MCPServerStatus{State: "not connected"}
	}
	st := a.rt.Status(context.Background(), name)
	return commands.MCPServerStatus{
		State:      st.State,
		Tools:      st.Tools,
		ToolsKnown: st.ToolsKnown,
		ToolNames:  st.ToolNames,
		Err:        st.Err,
		MissingEnv: st.MissingEnv,
	}
}
