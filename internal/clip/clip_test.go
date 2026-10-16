package clip

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCopyWayland(t *testing.T) {
	var got string
	backend, err := CopyWith(context.Background(), "hi", Options{
		Env: []string{"WAYLAND_DISPLAY=wayland-0"},
		Exec: func(_ context.Context, name string, args ...string) error {
			got = name
			return nil
		},
	})
	if err != nil || backend != "wl-copy" || got != "wl-copy" {
		t.Fatalf("%s %v %s", backend, err, got)
	}
}

func TestCopyXclipThenXsel(t *testing.T) {
	var names []string
	backend, err := CopyWith(context.Background(), "hi", Options{
		Env: []string{"DISPLAY=:0"},
		Exec: func(_ context.Context, name string, args ...string) error {
			names = append(names, name)
			if name == "xclip" {
				return errors.New("missing")
			}
			return nil
		},
	})
	if err != nil || backend != "xsel" {
		t.Fatalf("%s %v %v", backend, err, names)
	}
}

func TestCopyOSC52(t *testing.T) {
	var wrote []byte
	backend, err := CopyWith(context.Background(), "hi", Options{
		Env: []string{},
		Exec: func(context.Context, string, ...string) error {
			return errors.New("no bin")
		},
		TTY: "tty",
		Write: func(path string, data []byte) error {
			wrote = data
			return nil
		},
	})
	if err != nil || backend != "osc52" {
		t.Fatalf("%s %v", backend, err)
	}
	if !strings.Contains(string(wrote), "aGk=") {
		t.Fatalf("osc %q", wrote)
	}
}

func TestCopyOSC52TooBig(t *testing.T) {
	_, err := CopyWith(context.Background(), strings.Repeat("x", oscLimit+1), Options{
		Env:   []string{},
		Write: func(string, []byte) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "OSC 52") {
		t.Fatalf("%v", err)
	}
}

func TestCopyNone(t *testing.T) {
	_, err := CopyWith(context.Background(), "hi", Options{
		Env:  []string{},
		Exec: func(context.Context, string, ...string) error { return errors.New("x") },
		Write: func(string, []byte) error {
			return errors.New("no tty")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "wl-clipboard") {
		t.Fatalf("%v", err)
	}
}
