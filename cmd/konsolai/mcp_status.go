package main

import (
	"context"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/turn"
)

// mcpStatusAdapter maps the turn-owned MCP runtime to the commands status
// contract. It is display-only: no value, command line or header ever leaves
// the manager unredacted here.
type mcpStatusAdapter struct {
	rt *turn.MCPRuntime
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
