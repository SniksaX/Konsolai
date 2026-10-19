package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestGeneratedExamplesMatch(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	check := func(name, want string) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s differs from the generator output", name)
		}
	}
	check("config.example.yaml", InitYAML())
	check("config.default.yaml", InitYAML())
	check(".env.example", EnvExample())
}
