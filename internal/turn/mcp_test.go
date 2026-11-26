package turn

import (
	"context"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

func TestMCPRuntimeMapsProfileAndDisabledServer(t *testing.T) {
	profile := config.Builtin()
	server := config.DefaultMCPServer()
	server.Transport = "stdio"
	server.Command = "definitely-not-run"
	server.Enabled = false
	server.Env = map[string]string{"TOKEN": "${KONSOLAI_UNSET_FOR_MCP_TEST}"}
	profile.MCP.Servers = map[string]config.MCPServer{"off": server}
	rt, err := NewMCPRuntime(profile, t.TempDir(), "vtest")
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	if rt.Manager == nil || rt.Bridge == nil {
		t.Fatal("runtime did not build a manager and a bridge")
	}
	if tools, err := rt.ToolsFor(context.Background(), "answer", false, false, nil); err != nil || len(tools) != 0 {
		t.Fatalf("answer got MCP tools: %v %v", tools, err)
	}
	if tools, err := rt.ToolsFor(context.Background(), "build", false, false, nil); err != nil || len(tools) != 0 {
		t.Fatalf("disabled server exposed tools: %v %v", tools, err)
	}
	st := rt.Status(context.Background(), "off")
	if st.State != "disabled" {
		t.Fatalf("state %q", st.State)
	}
	if len(st.MissingEnv) != 1 || st.MissingEnv[0] != "KONSOLAI_UNSET_FOR_MCP_TEST" {
		t.Fatalf("missing env %v", st.MissingEnv)
	}
}

func TestMCPRuntimeWithoutServersIsSafe(t *testing.T) {
	rt, err := NewMCPRuntime(config.Builtin(), t.TempDir(), "vtest")
	if err != nil {
		t.Fatal(err)
	}
	if rt.Manager != nil {
		t.Fatal("no servers must not build a manager")
	}
	rt.Close()
	if tools, err := rt.ToolsFor(context.Background(), "build", false, false, nil); err != nil || tools != nil {
		t.Fatalf("empty runtime tools %v %v", tools, err)
	}
}
