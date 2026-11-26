package turn

import (
	"context"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/mcp"
	"github.com/SniksaX/Konsolai/internal/tools"
)

type countingMCP struct {
	lists map[string]int
}

func (c *countingMCP) Tools(_ context.Context, server string) ([]mcp.Tool, error) {
	c.lists[server]++
	return []mcp.Tool{{Name: "tool", Description: "d", ReadOnly: false}}, nil
}

func (c *countingMCP) Call(context.Context, string, string, map[string]any) (mcp.Result, error) {
	return mcp.Result{}, nil
}

func privateProfile() config.Config {
	p := config.Builtin()
	p.Models["remote"] = config.Model{Endpoint: "http://203.0.113.9/v1", Model: "m"}
	p.Roles.Main = "remote"
	priv := config.DefaultMCPServer()
	priv.Transport = "stdio"
	priv.Command = "x"
	priv.Private = true
	pub := config.DefaultMCPServer()
	pub.Transport = "stdio"
	pub.Command = "y"
	p.MCP.Servers = map[string]config.MCPServer{"priv": priv, "pub": pub}
	return p
}

func TestPrivateServerRefusedWhenMainRemote(t *testing.T) {
	profile := privateProfile()
	caller := &countingMCP{lists: map[string]int{}}
	rt := &MCPRuntime{Profile: profile, Bridge: tools.NewMCPBridge(caller, profile), MaxInline: 15}
	prompts := 0
	confirmed, err := rt.ToolsFor(context.Background(), "build", true, false, func(string) (bool, error) {
		prompts++
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if prompts != 1 {
		t.Fatalf("prompts %d", prompts)
	}
	if caller.lists["priv"] != 0 {
		t.Fatalf("private server was queried before the answer: %v", caller.lists)
	}
	if len(confirmed) != 1 {
		t.Fatalf("tools %v", confirmed)
	}
	// Refusal is remembered: no second prompt, still no private list.
	if _, err := rt.ToolsFor(context.Background(), "build", true, false, func(string) (bool, error) {
		prompts++
		return false, nil
	}); err != nil {
		t.Fatal(err)
	}
	if prompts != 1 || caller.lists["priv"] != 0 {
		t.Fatalf("prompts %d lists %v", prompts, caller.lists)
	}
}

func TestPrivateServerAllowedOnceAndYesSkipsPrompt(t *testing.T) {
	profile := privateProfile()
	caller := &countingMCP{lists: map[string]int{}}
	rt := &MCPRuntime{Profile: profile, Bridge: tools.NewMCPBridge(caller, profile), MaxInline: 15}
	prompts := 0
	allowed, err := rt.ToolsFor(context.Background(), "build", true, false, func(string) (bool, error) {
		prompts++
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if prompts != 1 || caller.lists["priv"] != 1 || len(allowed) != 2 {
		t.Fatalf("prompts %d lists %v tools %d", prompts, caller.lists, len(allowed))
	}
	if _, err := rt.ToolsFor(context.Background(), "build", true, false, nil); err != nil {
		t.Fatal(err)
	}
	if prompts != 1 {
		t.Fatalf("allow decision not remembered: %d prompts", prompts)
	}
	caller2 := &countingMCP{lists: map[string]int{}}
	rt2 := &MCPRuntime{Profile: profile, Bridge: tools.NewMCPBridge(caller2, profile), MaxInline: 15}
	if _, err := rt2.ToolsFor(context.Background(), "build", true, true, nil); err != nil {
		t.Fatal(err)
	}
	if caller2.lists["priv"] != 1 {
		t.Fatalf("--yes did not allow the private server: %v", caller2.lists)
	}
}
