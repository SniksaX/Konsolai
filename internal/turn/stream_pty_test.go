package turn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SniksaX/Konsolai/internal/agent"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/intent"
	"github.com/SniksaX/Konsolai/internal/llm"
	"github.com/SniksaX/Konsolai/internal/sysctx"
	"github.com/creack/pty"
)

func TestRunAnswerShowsLineBeforeNextChunk(t *testing.T) {
	saw := make(chan struct{})
	var once sync.Once
	var out lineSignal
	out.saw = saw
	out.once = &once
	out.needle = "alpha line one"

	var late atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "test", "max_model_len": 8192}},
			})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		writeEvent(w, fl, "alpha line one\n")
		select {
		case <-saw:
		case <-time.After(2 * time.Second):
			late.Store(true)
		}
		writeEvent(w, fl, "beta line two\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		if fl != nil {
			fl.Flush()
		}
	}))
	defer srv.Close()

	client := llm.New(srv.URL+"/v1", "test")
	r := testRunner(t, client, autoConfirm{})
	r.Out = &out
	r.Err = io.Discard
	code, answer, _, _, esc := r.runAnswer(context.Background(), client, client, sysctx.Snapshot{}, nil, nil,
		"how to use types", agent.AnswerPrompt, false,
		Request{Plain: true}, intent.Decision{Intent: intent.Answer}, 0)
	if code != 0 || esc != "" {
		t.Fatalf("code %d esc %q answer %q out %q", code, esc, answer, out.buf.String())
	}
	if late.Load() {
		t.Fatalf("first line was not on the writer before the second chunk\nout %q", out.buf.String())
	}
	if !strings.Contains(out.buf.String(), "alpha line one") || !strings.Contains(out.buf.String(), "beta line two") {
		t.Fatalf("out %q", out.buf.String())
	}
}

type lineSignal struct {
	buf    bytes.Buffer
	needle string
	saw    chan struct{}
	once   *sync.Once
}

func (w *lineSignal) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	if w.once != nil && strings.Contains(w.buf.String(), w.needle) {
		w.once.Do(func() { close(w.saw) })
	}
	return n, err
}

func TestAnswerStreamsBeforeNextLineOnPTY(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == "stream" {
		ptyStreamChild()
		return
	}
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}

	var mu sync.Mutex
	var send1, send2 time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "test", "max_model_len": 8192}},
			})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		time.Sleep(500 * time.Millisecond)
		mu.Lock()
		send1 = time.Now()
		mu.Unlock()
		writeEvent(w, fl, "alpha line one\n")
		time.Sleep(300 * time.Millisecond)
		mu.Lock()
		send2 = time.Now()
		mu.Unlock()
		writeEvent(w, fl, "beta line two\n")
		time.Sleep(300 * time.Millisecond)
		writeEvent(w, fl, "gamma line three\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		if fl != nil {
			fl.Flush()
		}
	}))
	defer srv.Close()

	cmd := exec.Command(os.Args[0], "-test.run=TestAnswerStreamsBeforeNextLineOnPTY")
	cmd.Env = append(os.Environ(), "KONSOLAI_PTY_CHILD=stream", "KONSOLAI_STREAM_URL="+srv.URL+"/v1")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = ptmx.Close()
	}()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})

	var screen bytes.Buffer
	var spinnerAt, lineAt time.Time
	var readMu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		tmp := make([]byte, 4096)
		for {
			n, err := ptmx.Read(tmp)
			if n > 0 {
				readMu.Lock()
				screen.Write(tmp[:n])
				now := time.Now()
				if spinnerAt.IsZero() && bytes.Contains(screen.Bytes(), []byte("thinking")) {
					spinnerAt = now
				}
				if lineAt.IsZero() && bytes.Contains(screen.Bytes(), []byte("alpha line one")) {
					lineAt = now
				}
				readMu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case err := <-waitErr:
		if err != nil {
			readMu.Lock()
			dump := screen.String()
			readMu.Unlock()
			t.Fatalf("child: %v\nscreen:\n%s", err, dump)
		}
	case <-time.After(8 * time.Second):
		readMu.Lock()
		dump := screen.String()
		readMu.Unlock()
		t.Fatalf("timeout\nscreen:\n%s", dump)
	}
	_ = ptmx.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
	}

	readMu.Lock()
	raw := append([]byte(nil), screen.Bytes()...)
	gotSpinner, gotLine := spinnerAt, lineAt
	readMu.Unlock()
	mu.Lock()
	s1, s2 := send1, send2
	mu.Unlock()

	plain := strings.ReplaceAll(string(raw), "\r", "")
	if gotSpinner.IsZero() || !gotSpinner.Before(s1) {
		t.Fatalf("spinner was not visible before the first token\nspinner=%s send1=%s\nscreen:\n%s", gotSpinner, s1, plain)
	}
	if gotLine.IsZero() || !gotLine.Before(s2) {
		t.Fatalf("first line was not on screen before the second line was sent\nline=%s send2=%s\nscreen:\n%s", gotLine, s2, plain)
	}
	if !strings.Contains(plain, "beta line two") {
		t.Fatalf("missing second line\nscreen:\n%s", plain)
	}
}

func ptyStreamChild() {
	url := os.Getenv("KONSOLAI_STREAM_URL")
	if url == "" {
		fmt.Fprint(os.Stderr, "missing stream url\n")
		os.Exit(1)
	}
	client := llm.New(url, "test")
	r := &Runner{
		Profile: config.Builtin(),
		Client:  client,
		Out:     os.Stdout,
		Err:     os.Stderr,
	}
	code, answer, _, _, esc := r.runAnswer(context.Background(), client, client, sysctx.Snapshot{}, nil, nil,
		"how to use types", agent.AnswerPrompt, false,
		Request{Plain: true}, intent.Decision{Intent: intent.Answer}, 0)
	if code != 0 || esc != "" || !strings.Contains(answer, "alpha line one") {
		fmt.Fprintf(os.Stderr, "code=%d esc=%s answer=%q\n", code, esc, answer)
		os.Exit(1)
	}
	os.Exit(0)
}

func writeEvent(w http.ResponseWriter, fl http.Flusher, text string) {
	payload, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{{
			"delta": map[string]any{"content": text},
		}},
	})
	_, _ = io.WriteString(w, "data: "+string(payload)+"\n\n")
	if fl != nil {
		fl.Flush()
	}
}
