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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/creack/pty"
)

var ansiReE2E = regexp.MustCompile(`\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]`)

func e2eScale() int {
	s := os.Getenv("KONSOLAI_TEST_TIMEOUT_SCALE")
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

func ptyTimeout() time.Duration {
	return 3 * time.Second * time.Duration(e2eScale())
}

func e2eRawTail(b []byte) string {
	if len(b) > 2048 {
		b = b[len(b)-2048:]
	}
	return strconv.Quote(string(b))
}

var (
	e2eBinOnce sync.Once
	e2eBin     string
	e2eBinErr  error
)

func e2eKonsolai(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("needs go")
	}
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
	s.expect("prompt", slashHas("›"))
	s.typeKeysWithin("touch the e2e confirm file\r", slashHas("[y]es"), ptyTimeout()*5)
	if !strings.Contains(s.screen().dump(), "run: touch") {
		s.failDump("want run: touch")
	}
	s.typeKeysWithin("y", slashHas("done"), ptyTimeout()*5)
	s.typeKeys("/exit\r", nil)
	s.waitExit(ptyTimeout())
}

func TestE2EProdClarificationThenN(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	srv := e2eLLM(t)
	s := startE2E(t, srv.URL+"/v1", t.TempDir())
	defer s.close()
	s.expect("prompt", slashHas("›"))
	s.typeKeysWithin("i'd like you to switch to prod\r", slashHas("Which one"), ptyTimeout()*5)
	s.typeKeysWithin("yes first one\r", slashHas("[y]es"), ptyTimeout()*5)
	if !strings.Contains(s.screen().dump(), "git checkout prod") {
		s.failDump("want checkout confirm")
	}
	s.typeKeysWithin("n", slashHas("done"), ptyTimeout()*5)
	s.typeKeys("/exit\r", nil)
	s.waitExit(ptyTimeout())
}

type e2eSess struct {
	t          *testing.T
	cmd        *exec.Cmd
	ptmx       *os.File
	mu         sync.Mutex
	buf        bytes.Buffer
	rows, cols int
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
		"INPUTRC=/dev/null",
		"TERM=xterm-256color",
		"NO_COLOR=1",
		"COLUMNS=100",
		"LINES=40",
	)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 100})
	s := &e2eSess{t: t, cmd: cmd, ptmx: ptmx, rows: 40, cols: 100}
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

func (s *e2eSess) raw() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.buf.Bytes()...)
}

func (s *e2eSess) failDump(why string) {
	s.t.Helper()
	sc := s.screen()
	s.t.Fatalf("%s\n--- screen ---\n%s\n--- raw ---\n%s", why, sc.dump(), e2eRawTail(s.raw()))
}

func slashHas(needle string) func(*slashScreen) bool {
	return func(sc *slashScreen) bool { return strings.Contains(sc.dump(), needle) }
}

func slashHasAll(parts ...string) func(*slashScreen) bool {
	return func(sc *slashScreen) bool {
		d := sc.dump()
		for _, p := range parts {
			if !strings.Contains(d, p) {
				return false
			}
		}
		return true
	}
}

func (s *e2eSess) expectWithin(why string, d time.Duration, ok func(*slashScreen) bool) *slashScreen {
	s.t.Helper()
	deadline := time.Now().Add(d)
	var last *slashScreen
	var raw []byte
	for time.Now().Before(deadline) {
		raw = s.raw()
		last = newSlashScreen(s.rows, s.cols)
		last.feed(raw)
		if ok(last) {
			return last
		}
		time.Sleep(15 * time.Millisecond)
	}
	dump := ""
	if last != nil {
		dump = last.dump()
	}
	s.t.Fatalf("timeout %s\n--- screen ---\n%s\n--- raw ---\n%s", why, dump, e2eRawTail(raw))
	return nil
}

func (s *e2eSess) expect(why string, ok func(*slashScreen) bool) *slashScreen {
	s.t.Helper()
	return s.expectWithin(why, ptyTimeout(), ok)
}

func splitE2EKeys(keys string) []string {
	b := []byte(keys)
	var out []string
	for i := 0; i < len(b); {
		if b[i] == 0x1b && i+2 < len(b) && b[i+1] == '[' {
			f := b[i+2]
			if f == 'A' || f == 'B' || f == 'C' || f == 'D' || f == 'F' || f == 'H' {
				out = append(out, string(b[i:i+3]))
				i += 3
				continue
			}
		}
		_, n := utf8.DecodeRune(b[i:])
		if n < 1 {
			n = 1
		}
		out = append(out, string(b[i:i+n]))
		i += n
	}
	return out
}

func (s *e2eSess) typeKeysWithin(keys string, done func(*slashScreen) bool, d time.Duration) {
	s.t.Helper()
	parts := splitE2EKeys(keys)
	for i, part := range parts {
		beforeN := len(s.raw())
		beforeDump := s.screen().dump()
		s.send(part)
		last := i == len(parts)-1
		s.expectWithin(strconv.Quote(part), d, func(sc *slashScreen) bool {
			moved := sc.dump() != beforeDump || len(s.raw()) > beforeN
			if !moved {
				return false
			}
			if last && done != nil {
				return done(sc)
			}
			return true
		})
	}
}

func (s *e2eSess) typeKeys(keys string, done func(*slashScreen) bool) {
	s.t.Helper()
	s.typeKeysWithin(keys, done, ptyTimeout())
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
			s.failDump("exit: " + err.Error())
		}
	case <-time.After(d):
		s.failDump("exit timeout")
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
