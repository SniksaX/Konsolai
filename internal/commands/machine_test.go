package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// machinePattern is the local-machine denylist. Pieces are split so this
// file does not itself match the pattern.
func machinePattern() *regexp.Regexp {
	parts := []string{
		"fat" + "boy",
		"sir" + "aj",
		"/ho" + "me/",
		"Desk" + "top/",
		"qw" + "en",
		"minist" + "ral",
		"bge-" + "m3",
		"114" + "36",
		"114" + "35",
		"95" + "01",
		"95" + "02",
		"554" + "32",
		"kp" + "ass",
		"zene" + "tys",
		"ly" + "ah",
		"endea" + "vour",
		"think" + "pad",
	}
	return regexp.MustCompile("(?i)" + strings.Join(parts, "|"))
}

// machineAllow excuses the public embed model id in files that must name it.
// Other denylist hits in those files still fail. The reason is the 1024-wide
// default whose vectors match memory_chunks.
var machineAllow = map[string]string{
	"compose.yaml":                   "public default embed model whose vectors are 1024-wide, matching memory_chunks",
	"README.md":                      "public default embed model whose vectors are 1024-wide, matching memory_chunks",
	"AGENTS.md":                      "public default embed model whose vectors are 1024-wide, matching memory_chunks",
	"internal/config/compose_env.go": "public default embed model whose vectors are 1024-wide, matching memory_chunks",
}

func TestNoLocalMachine(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	re := machinePattern()
	modelID := "bge-" + "m3"
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if !re.MatchString(text) {
			continue
		}
		_, allowed := machineAllow[name]
		for i, line := range strings.Split(text, "\n") {
			checked := line
			if allowed {
				checked = strings.ReplaceAll(checked, modelID, "")
			}
			if re.MatchString(checked) {
				t.Errorf("%s:%d matches the local-machine denylist", name, i+1)
			}
		}
	}
	for path, reason := range machineAllow {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("allowlist %s needs a reason", path)
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.ToLower(string(body)), modelID) {
			t.Errorf("allowlist %s does not contain the embed model id", path)
		}
	}
}
