package repl

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// P0-B scenario 9: welcome and diagnostics on a real PTY, plus the no-key sweep.

const (
	ptyModeWelcome = "p0b-welcome"
	ptyModeNoKey   = "p0b-nokey"
)

func ptyNoKeySecret() string {
	return "sk" + "-" + "p0b" + "-" + "noKey" + "-" + "7d3f9a1c5e80246b"
}

// TestPTYWelcomeCheckingThenStatuses: the first welcome paint says checking, the
// repaint replaces it with the real probe outcomes (auth failed, a latency, and
// an unreachable endpoint).
func TestPTYWelcomeCheckingThenStatuses(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == ptyModeWelcome {
		ptyChildRun()
		return
	}
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(authSrv.Close)
	embedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/embed" {
			time.Sleep(20 * time.Millisecond)
			_, _ = io.WriteString(w, `{}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(embedSrv.Close)

	sc := startPTYScenario(t, "TestPTYWelcomeCheckingThenStatuses", ptyModeWelcome, false, map[string]string{
		ptyWelcome:   "1",
		ptyFastURL:   authSrv.URL,
		ptyEmbedURL:  embedSrv.URL,
		ptyRerankURL: "http://127.0.0.1:1",
		ptySlowMS:    "1",
	})
	sc.v.expect("real probe statuses", screenHasAll("auth failed", "unreachable", "ms"))
	if raw := string(sc.raw()); !strings.Contains(raw, "checking") {
		t.Fatal("the first welcome paint never showed checking")
	}
	sc.prompt(t)

	sc.run(t, "/status", screenHasAll("auth failed", "ok"))
	sc.run(t, "/doctor", screenHas("auth failed"))
}

// TestPTYNoKeyOnAnySurface runs a provider that echoes the key back in a 401, then
// drives /status, /doctor and /config show. The key must never appear on screen
// or in any message body, while the Authorization header proves it was used.
func TestPTYNoKeyOnAnySurface(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == ptyModeNoKey {
		ptyChildRun()
		return
	}
	key := ptyNoKeySecret()
	sc := startPTYScenario(t, "TestPTYNoKeyOnAnySurface", ptyModeNoKey, false, map[string]string{
		ptyTrace:   "1",
		ptyMainKey: key,
	})
	sc.model.failChat = true
	sc.model.echoKey = key
	sc.prompt(t)

	sc.turn(t, "say hello", func(screen *screen) bool {
		return strings.Contains(screen.dump(), "[REDACTED]") && strings.Contains(screenPrompt(screen), "›")
	})
	sc.turn(t, "say it again", screenHas("done"))
	sc.run(t, "/status", screenHas("ok"))
	sc.run(t, "/doctor", screenHas("config file"))
	sc.run(t, "/config show", screenHas("max_inline_tools"))

	raw := string(sc.raw())
	if strings.Contains(raw, key) {
		t.Fatalf("the key appeared on the PTY screen:\n%s", ansiRe.ReplaceAllString(raw, ""))
	}
	for _, body := range sc.model.requestBodies() {
		if strings.Contains(body, key) {
			t.Fatal("a message body carried the key")
		}
	}
	auth := 0
	for _, h := range sc.model.requestHeaders() {
		if h.Get("Authorization") == "Bearer "+key {
			auth++
		}
		for name, values := range h {
			if name == "Authorization" {
				continue
			}
			for _, v := range values {
				if strings.Contains(v, key) {
					t.Fatalf("header %s carried the key", name)
				}
			}
		}
	}
	if auth == 0 {
		t.Fatal("the provider never saw the key in the Authorization header")
	}
}
