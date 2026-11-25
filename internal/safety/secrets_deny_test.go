package safety

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSecretsFileStaysDenied(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	secrets := filepath.Join(home, ".config", "konsolai", "secrets")
	if !SensitiveRead(secrets) {
		t.Fatal("the secrets file is not on the read denylist")
	}
	g, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.ResolveRead(secrets); !errors.Is(err, ErrSensitiveFile) {
		t.Fatalf("ResolveRead: %v", err)
	}
	if err := g.CheckCommandPaths("cat "+secrets, g.Base()); !errors.Is(err, ErrSensitiveFile) {
		t.Fatalf("shell read: %v", err)
	}
}
