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
	"testing"
	"time"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/turn"
	"github.com/creack/pty"
)

// The router reply is the assistant content, so its JSON is quoted once.
const remoteRouterReply = `{"choices":[{"message":{"role":"assistant","content":"{\"intent\":\"answer\",\"language\":\"en\",\"reason\":\"test\",\"reasoning\":false,\"effort\":\"low\",\"task\":\"other\",\"continues_session\":0,\"needs_memory\":false}"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`

const remoteStreamReply = "data: {\"choices\":[{\"delta\":{\"content\":\"REMOTE_OK\"},\"index\":0}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\n" +
	"data: [DONE]\n\n"

const remoteRemotePrompt = "sends your prompts and file contents to"

func TestRemoteAnswerWords(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"y", true}, {"Y", true}, {" yes ", true}, {"YES", true},
		{"n", false}, {"no", false}, {"", false}, {"yep", false}, {"maybe", false},
	}
	for _, c := range cases {
		if got := remoteAnswer(c.line); got != c.want {
			t.Fatalf("remoteAnswer(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

// remoteConfirmChild is the REPL process the PTY tests drive. The fake remote
// server answers the OpenAI calls and records every request it receives, so the
// parent can prove nothing was sent before the y/n answer.
func remoteConfirmChild() {
	logPath := os.Getenv("KONSOLAI_REMOTE_LOG")
	if logPath == "" {
		os.Stderr.WriteString("missing KONSOLAI_REMOTE_LOG")
		os.Exit(1)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		recordRemoteRequest(logPath, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"fake-model","max_model_len":4096}]}`)
			return
		}
		if strings.Contains(string(body), "Classify the user's request") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, remoteRouterReply)
			return
		}
		// The answer turn streams, so reply in SSE.
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, remoteStreamReply)
	}))
	defer srv.Close()

	// The gate looks at the runner profile, which names the unreachable
	// remote endpoint. The REPL profile stays local (and silent) so the
	// welcome probes never touch the fake server.
	remote := config.Builtin()
	remote.Models["vendor"] = config.Model{Endpoint: "http://203.0.113.9/v1"}
	remote.Roles.Main = "vendor"
	remote.Roles.Fast = ""
	remote.Roles.Embed = ""
	remote.Roles.Rerank = ""
	remote.Privacy.AllowRemote = true
	remote.RAG.Enabled = false
	remote.Database.URL = ""
	local := config.Builtin()
	local.UI.Banner = false
	local.UI.ClearOnStart = false
	local.Roles.Fast = ""
	local.Roles.Embed = ""
	local.Roles.Rerank = ""
	local.RAG.Enabled = false
	local.Database.URL = ""

	env := &commands.Env{
		Profile: local,
		Out:     os.Stdout,
		Err:     os.Stderr,
		Cwd:     os.Getenv("KONSOLAI_REMOTE_CWD"),
		Version: "vtest",
	}
	runner := &turn.Runner{
		Profile: remote,
		Client:  llm.New(srv.URL+"/v1", "fake-model"),
		Out:     os.Stdout,
		Err:     os.Stderr,
	}
	code := Run(context.Background(), Config{
		Runner:  runner,
		Env:     env,
		Reg:     commands.Default(),
		Version: "vtest",
		Model:   "fake-model",
		Out:     os.Stdout,
		Err:     os.Stderr,
	})
	os.Exit(code)
}

func recordRemoteRequest(path, route string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(f, route)
	_ = f.Close()
}

func remoteRequestCount(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(b)))
}

type remoteConfirmView struct {
	v       *ptyView
	pt      *os.File
	cmd     *exec.Cmd
	logPath string
}

func startRemoteConfirmPTY(t *testing.T, testName, mode string) *remoteConfirmView {
	t.Helper()
	home := t.TempDir()
	cwd := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "requests.log")
	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$")
	cmd.Env = append(os.Environ(),
		"KONSOLAI_PTY_CHILD="+mode,
		"KONSOLAI_REMOTE_LOG="+logPath,
		"KONSOLAI_REMOTE_CWD="+cwd,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"),
		"XDG_STATE_HOME="+filepath.Join(home, "state"),
		"COLUMNS=120",
		"LINES=40",
		"INPUTRC=/dev/null",
		"NO_COLOR=1",
	)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	v := startView(t, ptmx, 40, 120)
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120})
	r := &remoteConfirmView{v: v, pt: ptmx, cmd: cmd, logPath: logPath}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = ptmx.Close()
	})
	return r
}

func (r *remoteConfirmView) prompt(t *testing.T, why string) *screen {
	t.Helper()
	return r.v.expect(why, func(sc *screen) bool { return strings.Contains(screenPrompt(sc), "›") })
}

func (r *remoteConfirmView) ask(t *testing.T, text string) {
	t.Helper()
	r.prompt(t, "main prompt")
	r.v.typeKeys(text, screenHas(strings.TrimSuffix(text, "\r")))
	r.v.expect("remote prompt", screenHas(remoteRemotePrompt))
	time.Sleep(500 * time.Millisecond)
}

func (r *remoteConfirmView) noRequests(t *testing.T, when string) {
	t.Helper()
	if n := remoteRequestCount(r.logPath); n != 0 {
		failScreen(t, fmt.Sprintf("%d fake remote requests "+when, n), r.v.shot(), r.v.raw())
	}
}

func (r *remoteConfirmView) exit(t *testing.T) {
	t.Helper()
	r.prompt(t, "prompt after the turn")
	r.v.typeKeys("/exit\r", func(*screen) bool { return true })
	done := make(chan error, 1)
	go func() { done <- r.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			failScreen(t, "child: "+err.Error(), r.v.shot(), r.v.raw())
		}
	case <-time.After(ptyTimeout()):
		failScreen(t, "child still running after /exit", r.v.shot(), r.v.raw())
	}
}

func waitRemoteRequests(t *testing.T, r *remoteConfirmView, want int) {
	t.Helper()
	deadline := time.Now().Add(ptyTimeout())
	for time.Now().Before(deadline) {
		if remoteRequestCount(r.logPath) >= want {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	failScreen(t, fmt.Sprintf("fake remote server got %d requests, want %d", remoteRequestCount(r.logPath), want), r.v.shot(), r.v.raw())
}

func TestRemoteConfirmAllowsOnPTY(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == "remote-allow" {
		remoteConfirmChild()
		return
	}
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	r := startRemoteConfirmPTY(t, "TestRemoteConfirmAllowsOnPTY", "remote-allow")
	r.ask(t, "hi there\r")
	r.noRequests(t, " before the answer")
	r.v.typeKeys("y\r", screenHas("REMOTE_OK"))
	waitRemoteRequests(t, r, 1)
	r.exit(t)
	if n := remoteRequestCount(r.logPath); n == 0 {
		failScreen(t, "fake remote server never received the allowed call", r.v.shot(), r.v.raw())
	}
}

func TestRemoteConfirmDeclinesOnPTY(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == "remote-decline" {
		remoteConfirmChild()
		return
	}
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	r := startRemoteConfirmPTY(t, "TestRemoteConfirmDeclinesOnPTY", "remote-decline")
	r.ask(t, "hi there\r")
	r.noRequests(t, " before the answer")
	r.v.typeKeys("n\r", screenHas("remote use declined;"))
	r.noRequests(t, " after a typed n")
	// Take the mark after the first decline: no new confirmation prompt may
	// appear for the rest of the process.
	mark := len(r.v.raw())
	r.v.typeKeys("again\r", screenHas("remote use was declined for this process"))
	if tail := string(r.v.raw()[mark:]); strings.Contains(tail, remoteRemotePrompt) {
		failScreen(t, "the declined decision was not remembered (prompt shown again)", r.v.shot(), r.v.raw())
	}
	r.noRequests(t, " when the process remembers n")
	r.exit(t)
}

func TestRemoteConfirmCtrlCIsNoOnPTY(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == "remote-ctrlc" {
		remoteConfirmChild()
		return
	}
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	r := startRemoteConfirmPTY(t, "TestRemoteConfirmCtrlCIsNoOnPTY", "remote-ctrlc")
	r.ask(t, "hi there\r")
	r.noRequests(t, " before the answer")
	r.v.typeKeys("\x03", screenHas("remote use declined;"))
	if dump := r.v.shot().dump(); strings.Contains(dump, "remote confirmation:") {
		failScreen(t, "Ctrl+C was reported as a read error instead of n", r.v.shot(), r.v.raw())
	}
	r.noRequests(t, " after Ctrl+C at the prompt")
	r.exit(t)
}

// Guard the fake server payloads parse as JSON, so a typo fails here rather
// than deep inside the turn.
func TestRemoteConfirmFakeReplies(t *testing.T) {
	for _, body := range []string{remoteRouterReply} {
		var v struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatalf("bad fake reply %q: %v", body, err)
		}
		if len(v.Choices) != 1 || v.Choices[0].Message.Content == "" {
			t.Fatalf("bad fake reply %q", body)
		}
	}
	if !strings.Contains(remoteStreamReply, "REMOTE_OK") || !strings.Contains(remoteStreamReply, "[DONE]") {
		t.Fatalf("bad fake stream reply %q", remoteStreamReply)
	}
}
