package repl

import (
	"context"
	"encoding/json"
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
	"github.com/creack/pty"
)

func TestLateWelcomeUpdateKeepsTheFirstPrompt(t *testing.T) {
	if os.Getenv("KONSOLAI_PTY_CHILD") == "welcome-repaint" {
		welcomeRepaintChild()
		return
	}
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	home := t.TempDir()
	dir := filepath.Join(home, "app")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("defaults:\n  ui:\n    welcome: split\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestLateWelcomeUpdateKeepsTheFirstPrompt$")
	cmd.Env = append(os.Environ(),
		"KONSOLAI_PTY_CHILD=welcome-repaint",
		"HOME="+home,
		"KONSOLAI_THEME_CWD="+dir,
		"KONSOLAI_THEME_CFG="+cfg,
		"COLUMNS=120", "LINES=40", "INPUTRC=/dev/null", "NO_COLOR=1",
	)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = ptmx.Close()
		_ = cmd.Wait()
	}()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120})
	v := startView(t, ptmx, 40, 120)
	v.expect("welcome", screenHas("your terminal agent"))
	v.typeKeys("zqx", nil)
	time.Sleep(1200 * time.Millisecond)
	if dump := v.shot().dump(); !strings.Contains(dump, "zqx") {
		t.Fatalf("the late welcome update erased the first prompt line:\n%s", dump)
	}
}

func welcomeRepaintChild() {
	dir := os.Getenv("KONSOLAI_THEME_CWD")
	cfgPath := os.Getenv("KONSOLAI_THEME_CFG")
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(800 * time.Millisecond)
		if strings.HasSuffix(r.URL.Path, "/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "main-model", "max_model_len": 4096}}})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	prof := config.Builtin()
	prof.SetMain(slow.URL+"/v1", "main-model", false)
	prof.UI.ProbeDeadlineMS = 100
	prof.UI.StartupCapMS = 100
	prof.Roles.Fast = ""
	prof.UI.ClearOnStart = false
	prof.UI.Banner = true
	prof.UI.Welcome = "split"
	env := &commands.Env{
		Profile: prof, Out: os.Stdout, Err: os.Stderr, Cwd: dir,
		Version: "vtest", ConfigPath: cfgPath,
	}
	os.Exit(Run(context.Background(), Config{
		Env: env, Reg: commands.Default(), Version: "vtest", Model: "main-model",
		Out: os.Stdout, Err: os.Stderr,
	}))
}
