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

// machineAllow is empty on purpose. A tracked file that must match the
// denylist needs a path and a reason here.
var machineAllow = map[string]string{}

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
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" {
			continue
		}
		if _, ok := machineAllow[name]; ok {
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
		for i, line := range strings.Split(text, "\n") {
			if re.MatchString(line) {
				t.Errorf("%s:%d matches the local-machine denylist", name, i+1)
			}
		}
	}
	for path, reason := range machineAllow {
		if reason == "" {
			t.Errorf("allowlist %s needs a reason", path)
		}
	}
}
