package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingFile(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if f.DefaultProfile != "" || len(f.Profiles) != 0 {
		t.Fatalf("got %+v", f)
	}
}

func TestLoadInvalidJSONMentionsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"default_profile":`), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("want path in error, got %v", err)
	}
}

func TestLoadUnknownFieldRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"extra":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected unknown field error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("want path in error, got %v", err)
	}
}

func TestLoadUnknownProfileFieldRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"profiles":{"home":{"nope":1}}}`), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestResolveBuiltinDefault(t *testing.T) {
	t.Setenv("KONSOLAI_PROFILE", "")
	t.Setenv("KONSOLAI_DATABASE_URL", "")
	t.Setenv("KONSOLAI_ENDPOINT", "")
	t.Setenv("KONSOLAI_MODEL", "")
	p, err := File{}.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "default" || p.Endpoint != defaultEndpoint {
		t.Fatalf("got %+v", p)
	}
	if p.SessionIdleMinutes != 30 || p.RetentionDays != 90 {
		t.Fatalf("defaults %+v", p)
	}
}

func TestResolveUnknownProfile(t *testing.T) {
	_, err := File{}.Resolve("nope")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("got %v", err)
	}
}

func TestResolvePrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{
		"default_profile": "home",
		"profiles": {
			"home": {"database_url": "postgres://h/home", "endpoint": "http://home/v1", "model": "home-model", "session_idle_minutes": 10, "retention_days": 7},
			"work": {"database_url": "postgres://w/work", "endpoint": "http://work/v1", "model": "work-model"}
		}
	}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("default_profile", func(t *testing.T) {
		t.Setenv("KONSOLAI_PROFILE", "")
		t.Setenv("KONSOLAI_DATABASE_URL", "")
		t.Setenv("KONSOLAI_ENDPOINT", "")
		t.Setenv("KONSOLAI_MODEL", "")
		p, err := f.Resolve("")
		if err != nil {
			t.Fatal(err)
		}
		if p.Name != "home" || p.Model != "home-model" || p.Endpoint != "http://home/v1" {
			t.Fatalf("got %+v", p)
		}
		if p.SessionIdleMinutes != 10 || p.RetentionDays != 7 {
			t.Fatalf("got %+v", p)
		}
	})

	t.Run("env profile beats default_profile", func(t *testing.T) {
		t.Setenv("KONSOLAI_PROFILE", "work")
		t.Setenv("KONSOLAI_DATABASE_URL", "")
		t.Setenv("KONSOLAI_ENDPOINT", "")
		t.Setenv("KONSOLAI_MODEL", "")
		p, err := f.Resolve("")
		if err != nil {
			t.Fatal(err)
		}
		if p.Name != "work" || p.Model != "work-model" {
			t.Fatalf("got %+v", p)
		}
	})

	t.Run("named profile beats env", func(t *testing.T) {
		t.Setenv("KONSOLAI_PROFILE", "work")
		t.Setenv("KONSOLAI_DATABASE_URL", "")
		t.Setenv("KONSOLAI_ENDPOINT", "")
		t.Setenv("KONSOLAI_MODEL", "")
		p, err := f.Resolve("home")
		if err != nil {
			t.Fatal(err)
		}
		if p.Name != "home" || p.Model != "home-model" {
			t.Fatalf("got %+v", p)
		}
	})

	t.Run("env fields beat profile", func(t *testing.T) {
		t.Setenv("KONSOLAI_PROFILE", "")
		t.Setenv("KONSOLAI_DATABASE_URL", "postgres://env/db")
		t.Setenv("KONSOLAI_ENDPOINT", "http://env/v1")
		t.Setenv("KONSOLAI_MODEL", "env-model")
		p, err := f.Resolve("home")
		if err != nil {
			t.Fatal(err)
		}
		if p.DatabaseURL != "postgres://env/db" || p.Endpoint != "http://env/v1" || p.Model != "env-model" {
			t.Fatalf("got %+v", p)
		}
		if p.Location() != "env/db" {
			t.Fatalf("location %q", p.Location())
		}
	})
}

func TestLocationOmitsURL(t *testing.T) {
	p := Profile{DatabaseURL: "postgres://user:secret@localhost:5432/konsolai"}
	loc := p.Location()
	if strings.Contains(loc, "secret") || strings.Contains(loc, "postgres://") {
		t.Fatalf("leaked URL: %q", loc)
	}
	if loc != "localhost:5432/konsolai" {
		t.Fatalf("got %q", loc)
	}
}

func TestDisableLine(t *testing.T) {
	err := errors.New("store: ping: failed to connect to `user=x database=x`:\n\t127.0.0.1:1 dial tcp: connection refused")
	got := DisableLine("postgres://secret:hunter2@127.0.0.1:1/konsolai", err)
	if strings.Contains(got, "secret") || strings.Contains(got, "hunter2") || strings.Contains(got, "user=x") {
		t.Fatalf("leaked: %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("want one line: %q", got)
	}
	if !strings.HasPrefix(got, "memory disabled: cannot connect to 127.0.0.1:1:") {
		t.Fatalf("prefix: %q", got)
	}
}

func TestDefaultPathUsesXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	got := DefaultPath()
	if got != "/tmp/xdg/konsolai/config.json" {
		t.Fatalf("got %q", got)
	}
}
