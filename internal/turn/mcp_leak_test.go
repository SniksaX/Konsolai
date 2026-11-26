package turn

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/memory"
)

// Fixtures are assembled at runtime so secret scanners never flag a literal.
const (
	mcpLeakEnvVar  = "KONSOLAI_MCP_LEAK_SECRET"
	mcpLeakChild   = "KONSOLAI_TURN_MCP_LEAK_CHILD"
	mcpLeakToolTag = "leak__echo"
)

// mcpLeakValue is a distinctive fake credential shaped like the redactor's sk-
// rule so an unredacted copy is always recognisable.
func mcpLeakValue() string {
	return "sk" + "-" + "7b2e5d9c4a1f6083" + "e95a2c7d"
}

// TestMain runs the stdio fake MCP server in the re-executed test binary. The
// manager gives a stdio child an exact environment, so the child echoes the
// secret it received and the turn tests can prove it never reaches the model.
func TestMain(m *testing.M) {
	if os.Getenv(mcpLeakChild) == "1" {
		runMCPLeakChild()
		return
	}
	os.Exit(m.Run())
}

func runMCPLeakChild() {
	secret := os.Getenv("LEAK_TOKEN")
	srv := sdk.NewServer(&sdk.Implementation{Name: "leak", Version: "0.0.1"}, nil)
	srv.AddTool(&sdk.Tool{
		Name:        "echo",
		Description: "child env LEAK_TOKEN=" + secret,
		InputSchema: map[string]any{"type": "object"},
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "LEAK_TOKEN=" + secret}}}, nil
	})
	if err := srv.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		os.Exit(1)
	}
}

// mcpLeakModel is a fake OpenAI-compatible provider. It records every request
// body, calls the MCP tool on the first chat and answers on the next.
type mcpLeakModel struct {
	mu     sync.Mutex
	bodies []string
	chats  int
}

func (m *mcpLeakModel) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	chat := strings.HasSuffix(r.URL.Path, "/chat/completions")
	m.mu.Lock()
	m.bodies = append(m.bodies, string(body))
	n := m.chats
	if chat {
		m.chats++
	}
	m.mu.Unlock()
	if strings.HasSuffix(r.URL.Path, "/models") {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"leak-model","max_model_len":8192}]}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if n == 0 {
		_, _ = io.WriteString(w, remoteToolCallJSON)
		return
	}
	_, _ = io.WriteString(w, remoteAnswerJSON)
}

func (m *mcpLeakModel) requests() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.bodies...)
}

type mcpLeakKit struct {
	profile config.Config
	rt      *MCPRuntime
	model   *mcpLeakModel
	secret  string
	mode    string

	headerMu sync.Mutex
	header   string
}

func (k *mcpLeakKit) headerSeen() string {
	k.headerMu.Lock()
	defer k.headerMu.Unlock()
	return k.header
}

// newMCPLeakKit builds one turn's fake provider and one fake MCP server. In
// mode "env" the server is a stdio child whose env carries the secret; in mode
// "header" it is an in-process HTTP server whose Authorization header carries
// it. Both servers put the secret in a tool description and a tool result.
func newMCPLeakKit(t *testing.T, mode string) *mcpLeakKit {
	t.Helper()
	secret := mcpLeakValue()
	t.Setenv(mcpLeakEnvVar, secret)
	kit := &mcpLeakKit{secret: secret, mode: mode, model: &mcpLeakModel{}}
	modelSrv := httptest.NewServer(http.HandlerFunc(kit.model.handler))
	t.Cleanup(modelSrv.Close)
	profile := config.WithMain(modelSrv.URL+"/v1", "leak-model")
	profile.Roles.Fast = ""
	profile.Roles.Embed = ""
	profile.Roles.Rerank = ""
	profile.RAG.Enabled = false
	profile.Database.URL = ""
	server := config.DefaultMCPServer()
	switch mode {
	case "env":
		exe, err := os.Executable()
		if err != nil {
			t.Fatalf("executable: %v", err)
		}
		server.Transport = config.MCPTransportStdio
		server.Command = exe
		server.Env = map[string]string{
			mcpLeakChild: "1",
			"LEAK_TOKEN": "${" + mcpLeakEnvVar + "}",
		}
	case "header":
		handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server {
			return mcpLeakServer(secret)
		}, nil)
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			kit.headerMu.Lock()
			kit.header = r.Header.Get("Authorization")
			kit.headerMu.Unlock()
			handler.ServeHTTP(w, r)
		}))
		t.Cleanup(ts.Close)
		server.Transport = config.MCPTransportHTTP
		server.URL = ts.URL
		server.Headers = map[string]string{"Authorization": "Bearer ${" + mcpLeakEnvVar + "}"}
	default:
		t.Fatalf("unknown mode %q", mode)
	}
	profile.MCP.Servers = map[string]config.MCPServer{"leak": server}
	kit.profile = profile
	rt, err := NewMCPRuntime(profile, t.TempDir(), "vtest")
	if err != nil {
		t.Fatalf("mcp runtime: %v", err)
	}
	t.Cleanup(rt.Close)
	kit.rt = rt
	return kit
}

func mcpLeakServer(secret string) *sdk.Server {
	srv := sdk.NewServer(&sdk.Implementation{Name: "leak", Version: "0.0.1"}, nil)
	srv.AddTool(&sdk.Tool{
		Name:        "echo",
		Description: "auth header Authorization: Bearer " + secret,
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"token": map[string]any{"type": "string", "default": secret}},
		},
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "Authorization: Bearer " + secret + "\nLEAK_TOKEN=" + secret}}}, nil
	})
	return srv
}

// runMCPLeakTurn runs one act turn that calls the fake MCP tool.
func runMCPLeakTurn(t *testing.T, kit *mcpLeakKit, mem *memory.Guard) (Result, string, string) {
	t.Helper()
	var out, errb strings.Builder
	runner := leakTurnRunner(t, kit.profile, mem, &out, &errb)
	runner.MCPRuntime = kit.rt
	res := runner.RunTurn(context.Background(), Request{
		Text: "use the mcp echo tool", Intent: "act", Yes: true,
		Trace: true, Cwd: t.TempDir(), New: true,
	})
	return res, out.String(), errb.String()
}

// requireMCPCall proves the turn really called the MCP tool: the provider saw
// the call and its masked result, and the trace counted one MCP call.
func requireMCPCall(t *testing.T, kit *mcpLeakKit, res Result, out, errb string) string {
	t.Helper()
	if res.ExitCode != 0 {
		t.Fatalf("exit %d: %s", res.ExitCode, errb)
	}
	requests := kit.model.requests()
	joined := strings.Join(requests, "\n")
	if len(requests) == 0 {
		t.Fatal("the fake provider saw no request")
	}
	if !strings.Contains(joined, mcpLeakToolTag) {
		t.Fatalf("the provider saw no MCP call: %s", clipForTest(joined))
	}
	if !strings.Contains(joined, "LEAK_TOKEN=") || (!strings.Contains(joined, "[REDACTED") && !strings.Contains(joined, "[masked")) {
		t.Fatalf("the provider did not receive the masked MCP result: %s", clipForTest(joined))
	}
	if !strings.Contains(out, "mcp leak:echo") {
		t.Fatalf("the turn did not report the MCP call: %q", out)
	}
	if !strings.Contains(errb, "mcp=1") {
		t.Fatalf("the trace did not count the MCP call: %q", errb)
	}
	return joined
}

// traceFiles reads every trace dump the turn wrote under XDG_STATE_HOME.
func traceFiles(t *testing.T) string {
	t.Helper()
	root := filepath.Join(os.Getenv("XDG_STATE_HOME"), "konsolai", "trace")
	var b strings.Builder
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr == nil {
			b.Write(body)
			b.WriteByte('\n')
		}
		return nil
	})
	return b.String()
}

func assertMCPLeakFree(t *testing.T, where, secret, text string) {
	t.Helper()
	if strings.Contains(text, secret) {
		t.Fatalf("%s carries the configured MCP secret", where)
	}
}

// clipForTest bounds a failure message so a leak report never dumps a whole
// prompt on the terminal.
func clipForTest(s string) string {
	if len(s) > 280 {
		return s[:280] + "..."
	}
	return s
}

// TestTraceMCPCallKeepsTheSecretOut is leak surface 1: --trace output for a turn
// with an MCP tool call, including the dumpTrace files, never carries the
// server's env or header secret. The trace must still report the call.
func TestTraceMCPCallKeepsTheSecretOut(t *testing.T) {
	for _, mode := range []string{"env", "header"} {
		t.Run(mode, func(t *testing.T) {
			kit := newMCPLeakKit(t, mode)
			res, out, errb := runMCPLeakTurn(t, kit, memory.NewGuard(nil, nil))
			requireMCPCall(t, kit, res, out, errb)
			assertMCPLeakFree(t, "trace stderr", kit.secret, errb)
			assertMCPLeakFree(t, "trace dump", kit.secret, traceFiles(t))
		})
	}
}

// TestModelRequestsFromMCPCallKeepTheSecretOut is leak surface 5: the fake
// provider captures every request body, the tool list included, and none of
// them carries the server's env or header secret.
func TestModelRequestsFromMCPCallKeepTheSecretOut(t *testing.T) {
	for _, mode := range []string{"env", "header"} {
		t.Run(mode, func(t *testing.T) {
			kit := newMCPLeakKit(t, mode)
			res, out, errb := runMCPLeakTurn(t, kit, memory.NewGuard(nil, nil))
			joined := requireMCPCall(t, kit, res, out, errb)
			assertMCPLeakFree(t, "the model request bodies", kit.secret, joined)
			if mode == "header" {
				if got := kit.headerSeen(); got != "Bearer "+kit.secret {
					t.Fatalf("the fake server did not receive the configured header: %q", got)
				}
			}
		})
	}
}

// TestMCPOutputAndTraceBufferKeepTheSecretOut is leak surface 6. Konsolai has
// no logging facility, so the turn's diagnostics are its Out and Err writers
// and the trace buffer: none of them may carry the secret.
func TestMCPOutputAndTraceBufferKeepTheSecretOut(t *testing.T) {
	for _, mode := range []string{"env", "header"} {
		t.Run(mode, func(t *testing.T) {
			kit := newMCPLeakKit(t, mode)
			res, out, errb := runMCPLeakTurn(t, kit, memory.NewGuard(nil, nil))
			requireMCPCall(t, kit, res, out, errb)
			for _, line := range strings.Split(out+"\n"+errb, "\n") {
				if strings.Contains(line, kit.secret) {
					t.Fatalf("a diagnostic line carries the MCP secret: %q", line)
				}
			}
			assertMCPLeakFree(t, "the trace buffer", kit.secret, traceFiles(t))
		})
	}
}

// TestStoreTranscriptsFromMCPCallKeepTheSecretOut is leak surface 4: a
// persisted turn with an MCP call leaves no secret in the transcripts API or
// any store text row.
func TestStoreTranscriptsFromMCPCallKeepTheSecretOut(t *testing.T) {
	st, q := openLeakDB(t)
	for _, mode := range []string{"env", "header"} {
		t.Run(mode, func(t *testing.T) {
			kit := newMCPLeakKit(t, mode)
			guard := memory.NewGuard(st, func(error) {})
			res, out, errb := runMCPLeakTurn(t, kit, guard)
			requireMCPCall(t, kit, res, out, errb)
			assertMCPLeakFree(t, "turn output", kit.secret, out)
			assertMCPLeakFree(t, "turn diagnostics", kit.secret, errb)
			if res.SessionID == "" {
				t.Fatal("the turn did not create a session")
			}
			msgs, err := st.LoadMessages(context.Background(), res.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			var b strings.Builder
			for _, m := range msgs {
				b.WriteString(m.Content)
				b.WriteByte('\n')
				b.Write(m.ToolCalls)
				b.WriteByte('\n')
			}
			if !strings.Contains(b.String(), "the tool answered") {
				t.Fatalf("the assistant answer was not persisted: %q", b.String())
			}
			if !strings.Contains(b.String(), "LEAK_TOKEN=") || (!strings.Contains(b.String(), "[REDACTED") && !strings.Contains(b.String(), "[masked")) {
				t.Fatalf("the masked MCP result was not persisted: %s", clipForTest(b.String()))
			}
			assertMCPLeakFree(t, "the transcripts API", kit.secret, b.String())
			waitTurnStats(t, q)
			assertNoKeyInStoreRows(t, q, kit.secret)
		})
	}
}

// The fake provider's replies, so a typo fails at compile time and not inside a
// JSON decode.
const (
	remoteAnswerJSON   = `{"choices":[{"message":{"role":"assistant","content":"the tool answered"}}],"usage":{"prompt_tokens":9,"completion_tokens":4}}`
	remoteToolCallJSON = `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"` + mcpLeakToolTag + `","arguments":"{}"}}]}}],"usage":{"prompt_tokens":9,"completion_tokens":3}}`
)
