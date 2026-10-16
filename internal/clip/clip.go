package clip

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const oscLimit = 100 * 1024

type ExecFunc func(ctx context.Context, name string, args ...string) error

type Options struct {
	Env   []string
	Exec  ExecFunc
	TTY   string
	Write func(path string, data []byte) error
}

func Copy(ctx context.Context, text string) (string, error) {
	return CopyWith(ctx, text, Options{})
}

func CopyWith(ctx context.Context, text string, opt Options) (string, error) {
	env := opt.Env
	if env == nil {
		env = os.Environ()
	}
	get := func(k string) string {
		for _, e := range env {
			if key, val, ok := strings.Cut(e, "="); ok && key == k {
				return val
			}
		}
		return ""
	}
	run := opt.Exec
	if run == nil {
		run = func(ctx context.Context, name string, args ...string) error {
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Stdin = strings.NewReader(text)
			cmd.Env = env
			return cmd.Run()
		}
	}
	if get("WAYLAND_DISPLAY") != "" {
		if err := run(ctx, "wl-copy"); err == nil {
			return "wl-copy", nil
		}
	}
	if get("DISPLAY") != "" {
		if err := run(ctx, "xclip", "-selection", "clipboard"); err == nil {
			return "xclip", nil
		}
		if err := run(ctx, "xsel", "-ib"); err == nil {
			return "xsel", nil
		}
	}
	if err := writeOSC52(text, opt); err == nil {
		return "osc52", nil
	} else if err != nil && !errors.Is(err, errNoTTY) {
		return "", err
	}
	return "", errors.New("no clipboard available: install wl-clipboard (Wayland) or xclip (X11)")
}

var errNoTTY = errors.New("no tty")

func writeOSC52(text string, opt Options) error {
	if len(text) > oscLimit {
		return fmt.Errorf("clipboard: text is %d bytes; OSC 52 refuses more than %d bytes", len(text), oscLimit)
	}
	tty := opt.TTY
	if tty == "" {
		tty = "/dev/tty"
	}
	write := opt.Write
	if write == nil {
		write = func(path string, data []byte) error {
			f, err := os.OpenFile(path, os.O_WRONLY, 0)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = f.Write(data)
			return err
		}
	}
	payload := base64.StdEncoding.EncodeToString([]byte(text))
	seq := []byte("\033]52;c;" + payload + "\a")
	if err := write(tty, seq); err != nil {
		return errNoTTY
	}
	return nil
}
