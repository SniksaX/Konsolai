package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

var ansiReE2E = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

var (
	e2eBinOnce sync.Once
	e2eBin     string
	e2eBinErr  error
)

func e2eKonsolai(t *testing.T) string {
	t.Helper()
	e2eBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "konsolai-e2e-bin")
		if err != nil {
			e2eBinErr = err
			return
		}
		e2eBin = filepath.Join(dir, "konsolai")
		cmd := exec.Command("go", "build", "-o", e2eBin, ".")
		out, err := cmd.CombinedOutput()
		if err != nil {
			e2eBinErr = err
			e2eBin = string(out)
		}
	})
	if e2eBinErr != nil {
		t.Fatalf("build konsolai: %v\n%s", e2eBinErr, e2eBin)
	}
	return e2eBin
}

func TestE2EConfirmSingleY(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	srv := e2eLLM(t)
	s := startE2E(t, srv.URL+"/v1", t.TempDir())
	defer s.close()
	mark := s.wait("›", 10*time.Second)
	s.send("touch the e2e confirm file\r")
	s.waitNew(mark, "[y]es", 15*time.Second)
	if !strings.Contains(s.plain(), "run: touch") {
		t.Fatalf("want run: touch, got\n%s", s.plain())
	}
	mark = s.len()
	s.send("y")
	s.waitNew(mark, "done", 10*time.Second)
	s.send("/exit\r")
	s.waitExit(5 * time.Second)
}

func TestE2EProdClarificationThenN(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	srv := e2eLLM(t)
	s := startE2E(t, srv.URL+"/v1", t.TempDir())
	defer s.close()
	mark := s.wait("›", 10*time.Second)
	s.send("i'd like you to switch to prod\r")
	s.waitNew(mark, "Which one", 15*time.Second)
	mark = s.len()
	s.send("yes first one\r")
	s.waitNew(mark, "[y]es", 15*time.Second)
	if !strings.Contains(s.plain(), "git checkout prod") {
		t.Fatalf("want checkout confirm, got\n%s", s.plain())
	}
	mark = s.len()
	s.send("n")
	s.waitNew(mark, "done", 15*time.Second)
	s.send("/exit\r")
	s.waitExit(5 * time.Second)
}

type e2eSess struct {
	t    *testing.T
	cmd  *exec.Cmd
	ptmx *os.File
	mu   sync.Mutex
	buf  bytes.Buffer
}

func startE2E(t *testing.T, endpoint, cwd string) *e2eSess {
	t.Helper()
	xdg := t.TempDir()
	cfgDir := filepath.Join(xdg, "konsolai")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := "default_profile: default\n" +
		"defaults:\n" +
		"  llm:\n" +
		"    endpoint: " + endpoint + "\n" +
		"    model: test\n" +
		"  fast:\n" +
		"    enabled: false\n" +
		"    endpoint: " + endpoint + "\n" +
		"  database:\n" +
		"    url: \"\"\n" +
		"  rag:\n" +
		"    enabled: false\n" +
		"  ui:\n" +
		"    banner: false\n" +
		"    clear_on_start: false\n" +
		"    theme: dark\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(e2eKonsolai(t), "--plain")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+xdg,
		"TERM=xterm-256color",
		"NO_COLOR=1",
		"COLUMNS=80",
		"LINES=24",
	)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})
	s := &e2eSess{t: t, cmd: cmd, ptmx: ptmx}
	go func() {
		tmp := make([]byte, 4096)
		for {
			n, err := ptmx.Read(tmp)
			if n > 0 {
				s.mu.Lock()
				s.buf.Write(tmp[:n])
				s.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return s
}

func (s *e2eSess) plain() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw := s.buf.String()
	raw = strings.ReplaceAll(raw, "\r", "")
	return ansiReE2E.ReplaceAllString(raw, "")
}

func (s *e2eSess) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Len()
}

func (s *e2eSess) wait(needle string, d time.Duration) int {
	s.t.Helper()
	s.waitNew(0, needle, d)
	return s.len()
}

func (s *e2eSess) waitNew(from int, needle string, d time.Duration) {
	s.t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		raw := append([]byte(nil), s.buf.Bytes()[min(from, s.buf.Len()):]...)
		s.mu.Unlock()
		plain := ansiReE2E.ReplaceAllString(strings.ReplaceAll(string(raw), "\r", ""), "")
		if strings.Contains(plain, needle) {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	s.t.Fatalf("timeout waiting for %q after %d\n%s", needle, from, s.plain())
}

func (s *e2eSess) send(keys string) {
	s.t.Helper()
	if _, err := s.ptmx.Write([]byte(keys)); err != nil {
		s.t.Fatal(err)
	}
}

func (s *e2eSess) waitExit(d time.Duration) {
	s.t.Helper()
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			s.t.Fatalf("exit: %v\n%s", err, s.plain())
		}
	case <-time.After(d):
		s.t.Fatalf("exit timeout\n%s", s.plain())
	}
}

func (s *e2eSess) close() {
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.ptmx.Close()
}

func e2eLLM(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "models") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "test", "max_model_len": 8192}},
			})
			return
		}
		body, _ := io.ReadAll(r.Body)
		s := string(body)
		w.Header().Set("Content-Type", "application/json")
		encode := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		if strings.Contains(s, "response_format") || strings.Contains(s, "json_schema") {
			encode(e2eChat(`{"intent":"act","language":"en","reason":"do","reasoning":false,"task":"other","continues_session":0}`))
			return
		}
		low := strings.ToLower(s)
		if strings.Contains(low, "reject") || strings.Contains(s, `"role":"tool"`) || strings.Contains(s, `"role": "tool"`) {
			encode(e2eTool("finish", `{"summary":"done"}`))
			return
		}
		if strings.Contains(low, "yes first one") || strings.Contains(s, "previous_exchange") {
			encode(e2eTool("run_shell", `{"cmd":"git checkout prod"}`))
			return
		}
		if strings.Contains(low, "switch to prod") {
			encode(e2eTool("finish", `{"summary":"Which one: 1) git checkout prod 2) stay on this branch?"}`))
			return
		}
		encode(e2eTool("run_shell", `{"cmd":"touch e2e-confirm.txt"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func e2eChat(content string) map[string]any {
	return map[string]any{
		"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": content}}},
	}
}

func e2eTool(name, args string) map[string]any {
	return map[string]any{
		"choices": []map[string]any{{"message": map[string]any{
			"role": "assistant",
			"tool_calls": []map[string]any{{
				"id": "1", "type": "function",
				"function": map[string]any{"name": name, "arguments": args},
			}},
		}}},
	}
}
