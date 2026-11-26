package repl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/turn"
	"github.com/SniksaX/Konsolai/internal/ui"
	"github.com/creack/pty"
)

const v050Prompt = "sends your prompts and file contents to"

func scenarioChat(role, content string) string {
	b, _ := json.Marshal(content)
	return fmt.Sprintf(`{"choices":[{"message":{"role":%q,"content":%s}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`, role, b)
}

func scenarioTool(id, name, args string) string {
	a, _ := json.Marshal(args)
	return fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":%q,"type":"function","function":{"name":%q,"arguments":%s}}]}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`, id, name, a)
}

func scenarioRouter(intent string) string {
	return scenarioChat("assistant", fmt.Sprintf(`{"intent":%q,"language":"en","reason":"test","reasoning":false,"effort":"low","task":"other","continues_session":0,"needs_memory":false}`, intent))
}

// scenarioChild is the REPL process the P0-A PTY scenarios drive. Scenario
// selection is KONSOLAI_SCENARIO; every provider is a strict local fake.
func scenarioChild() {
	mode := os.Getenv("KONSOLAI_SCENARIO")
	logPath := os.Getenv("KONSOLAI_REMOTE_LOG")
	cwd := os.Getenv("KONSOLAI_PANEL_CWD")
	if cwd == "" {
		cwd = "."
	}
	var mu sync.Mutex
	chat := 0
	answer := "FAST_OK"
	replyMode := mode
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"fake-model","max_model_len":16384}]}`)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		if strings.Contains(body, "Classify the user's request") {
			intent := "act"
			if replyMode == "panel" {
				intent = "answer"
			}
			if strings.Contains(body, "\"hi\"") || strings.Contains(body, "hi\n") {
				intent = "answer"
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, scenarioRouter(intent))
			return
		}
		mu.Lock()
		n := chat
		chat++
		mu.Unlock()
		if logPath != "" {
			if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
				_, _ = f.WriteString("chat\n")
				_ = f.Close()
			}
		}
		switch replyMode {
		case "ratelimit-long":
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"rate limited by the provider"}}`)
		case "ratelimit-short":
			if n == 0 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, `{"error":{"message":"rate limited by the provider"}}`)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"SHORT_RETRY_OK\"}}]}\n\ndata: [DONE]\n\n")
		case "config":
			if n == 0 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, scenarioTool("1", "write_file", `{"path":"config.yaml","content":"x: 1"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, scenarioTool("2", "finish", `{"summary":"done"}`))
		case "gate", "config-gate":
			if strings.Contains(body, `"tools"`) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, scenarioTool("1", "finish", `{"summary":"GATE_OK"}`))
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			payload, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": "FAST_OK"}}}})
			_, _ = io.WriteString(w, "data: "+string(payload)+"\n\ndata: [DONE]\n\n")
		case "panel":
			lines := answer
			w.Header().Set("Content-Type", "text/event-stream")
			payload, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": lines}}}})
			_, _ = io.WriteString(w, "data: "+string(payload)+"\n\ndata: [DONE]\n\n")
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, scenarioTool("1", "finish", `{"summary":"DONE_OK"}`))
		}
	}))
	defer srv.Close()

	remote := config.Builtin()
	remote.Models["vendor"] = config.Model{Endpoint: "http://203.0.113.9/v1", Model: "fake-model"}
	remote.Roles.Main = "vendor"
	remote.Roles.Fast = ""
	remote.Roles.Embed = ""
	remote.Roles.Rerank = ""
	remote.Privacy.AllowRemote = true
	remote.RAG.Enabled = false
	remote.Database.URL = ""
	remote.UI.Banner = false
	remote.UI.ClearOnStart = false

	local := config.Builtin()
	local.UI.AnswerPanel = true
	local.UI.Banner = false
	local.UI.ClearOnStart = false
	local.Roles.Fast = ""
	local.Roles.Embed = ""
	local.Roles.Rerank = ""
	local.RAG.Enabled = false
	local.Database.URL = ""

	// Gate mode uses a fast local answer client plus a remote main for the real
	// request; every other mode is fully local.
	if mode == "gate" {
		remote.RAG.Enabled = false
	}
	env := &commands.Env{Profile: local, Out: os.Stdout, Err: os.Stderr, Cwd: cwd, Version: "vtest"}
	runner := &turn.Runner{Profile: local, Client: llm.New(srv.URL+"/v1", "fake-model"), Out: os.Stdout, Err: os.Stderr}
	if mode == "gate" {
		runner.Profile = remote
		runner.FastClient = llm.New(srv.URL+"/v1", "fake-model")
	}
	ui.ApplyUIOpts(local.UI.Theme, local.UI.Appearance, false, true, false)
	code := Run(context.Background(), Config{
		Runner: runner, Env: env, Reg: commands.Default(),
		Version: "vtest", Model: "fake-model", Out: os.Stdout, Err: os.Stderr,
	})
	os.Exit(code)
}

func startScenarioPTY(t *testing.T, testName, scenario, cwd string) *remoteConfirmView {
	t.Helper()
	home := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "requests.log")
	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$")
	var base []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "NO_COLOR=") || strings.HasPrefix(kv, "TERM=") || strings.HasPrefix(kv, "COLORTERM=") {
			continue
		}
		base = append(base, kv)
	}
	cmd.Env = append(base,
		"KONSOLAI_PTY_CHILD=v050-"+scenario,
		"KONSOLAI_SCENARIO="+scenario,
		"KONSOLAI_REMOTE_LOG="+logPath,
		"KONSOLAI_PANEL_CWD="+cwd,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"),
		"XDG_STATE_HOME="+filepath.Join(home, "state"),
		"COLUMNS=120", "LINES=40", "INPUTRC=/dev/null", "TERM=xterm-256color", "COLORTERM=truecolor",
	)
	cmd.Env = append(strings.Split(os.Getenv("KONSOLAI_EXTRA_ENV"), "\n"), cmd.Env...)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	v := startView(t, ptmx, 40, 120)
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120})
	r := &remoteConfirmView{v: v, pt: ptmx, cmd: cmd, logPath: logPath}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = ptmx.Close() })
	return r
}

func TestV050GreetingFastNoPrompt(t *testing.T) {
	if os.Getenv("KONSOLAI_SCENARIO") == "gate" {
		scenarioChild()
		return
	}
	r := startScenarioPTY(t, "TestV050GreetingFastNoPrompt", "gate", t.TempDir())
	r.v.typeKeys("hi\r", screenHas("FAST_OK"))
	if strings.Contains(r.v.shot().dump(), v050Prompt) {
		failScreen(t, "greeting asked for remote confirmation", r.v.shot(), r.v.raw())
	}
	r.exit(t)
}

func TestV050ConfigProtectionPTY(t *testing.T) {
	if os.Getenv("KONSOLAI_SCENARIO") == "config" {
		scenarioChild()
		return
	}
	project := t.TempDir()
	real := filepath.Join(project, ".real", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("defaults: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(project, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KONSOLAI_CONFIG", real)
	r := startScenarioPTY(t, "TestV050ConfigProtectionPTY", "config", project)
	r.v.typeKeys("edit config.yaml to add an mcp server\r", screenHas("changed only through /set"))
	body, _ := os.ReadFile(real)
	if string(body) != "defaults: {}\n" {
		failScreen(t, "the config changed through the model", r.v.shot(), r.v.raw())
	}
	r.exit(t)
}

func TestV050RateLimitLongStops(t *testing.T) {
	if os.Getenv("KONSOLAI_SCENARIO") == "ratelimit-long" {
		scenarioChild()
		return
	}
	r := startScenarioPTY(t, "TestV050RateLimitLongStops", "ratelimit-long", t.TempDir())
	r.prompt(t, "main prompt")
	r.v.typeKeys("rename a.txt to b.txt\r", nil)
	r.v.expect("provider message plus explanation", func(sc *screen) bool {
		d := sc.dump()
		return strings.Contains(d, "too long") || strings.Contains(d, "rate limited")
	})
	r.exit(t)
}
