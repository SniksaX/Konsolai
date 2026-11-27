package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSensitiveReasonAndDiffDoNotLeakSecrets(t *testing.T) {
	secret := "sk-" + strings.Repeat("c", 24)
	reg, root := sensitiveRegistry(t)
	old := `{"scripts":{"build":"tsc"}}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	act, err := prepareTool(t, reg, "edit_file",
		`{"path":"package.json","old_str":"\"build\":\"tsc\"","new_str":"\"build\":\"deploy --token `+secret+`\""}`)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := act.(interface{ SensitiveReason() string })
	if !ok || !strings.Contains(s.SensitiveReason(), "package.json") {
		t.Fatalf("want a sensitive reason, got %v", act)
	}
	if strings.Contains(s.SensitiveReason(), secret) {
		t.Fatalf("reason leaks the secret: %q", s.SensitiveReason())
	}
	// A secret-shaped extra pattern is masked in the reason too.
	regExtra, _ := sensitiveRegistry(t, "**/"+secret+"/**")
	act, err = prepareTool(t, regExtra, "write_file", `{"path":"`+secret+`/x.sh","content":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	s, ok = act.(interface{ SensitiveReason() string })
	if !ok || s.SensitiveReason() == "" {
		t.Fatal("want an extra-pattern sensitive reason")
	}
	if strings.Contains(s.SensitiveReason(), secret) {
		t.Fatalf("extra-pattern reason leaks the secret: %q", s.SensitiveReason())
	}
}
