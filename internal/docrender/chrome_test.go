package docrender

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFindChromeEnvWins(t *testing.T) {
	getenv = func(k string) string {
		if k == "KONSOLAI_CHROME" {
			return "/opt/chrome"
		}
		return ""
	}
	lookPath = func(string) (string, error) { return "/usr/bin/chromium", nil }
	t.Cleanup(restoreChromeHooks)
	p, err := FindChrome()
	if err != nil || p != "/opt/chrome" {
		t.Fatalf("%q %v", p, err)
	}
}

func TestFindChromeLookPathOrder(t *testing.T) {
	getenv = func(string) string { return "" }
	var seen []string
	lookPath = func(name string) (string, error) {
		seen = append(seen, name)
		if name == "google-chrome-stable" {
			return "/usr/bin/google-chrome-stable", nil
		}
		return "", errors.New("missing")
	}
	userHome = func() (string, error) { return "/nope", nil }
	t.Cleanup(restoreChromeHooks)
	p, err := FindChrome()
	if err != nil || p != "/usr/bin/google-chrome-stable" {
		t.Fatalf("%q %v", p, err)
	}
	want := []string{"chromium", "chromium-browser", "google-chrome-stable"}
	if len(seen) < 3 || seen[0] != want[0] || seen[1] != want[1] || seen[2] != want[2] {
		t.Fatalf("order %v", seen)
	}
}

func TestFindChromePuppeteerGlobNewest(t *testing.T) {
	getenv = func(string) string { return "" }
	lookPath = func(string) (string, error) { return "", errors.New("missing") }
	userHome = func() (string, error) { return "/home/t", nil }
	glob = func(pattern string) ([]string, error) {
		if !filepath.IsAbs(pattern) && pattern == "" {
			t.Fatal("empty pattern")
		}
		return []string{
			"/home/t/.cache/puppeteer/chrome/linux-120.0.0.0/chrome-linux64/chrome",
			"/home/t/.cache/puppeteer/chrome/linux-155.0.8059.79/chrome-linux64/chrome",
		}, nil
	}
	t.Cleanup(restoreChromeHooks)
	p, err := FindChrome()
	if err != nil || p != "/home/t/.cache/puppeteer/chrome/linux-155.0.8059.79/chrome-linux64/chrome" {
		t.Fatalf("%q %v", p, err)
	}
}

func TestFindChromeMissing(t *testing.T) {
	getenv = func(string) string { return "" }
	lookPath = func(string) (string, error) { return "", errors.New("missing") }
	userHome = func() (string, error) { return "/home/t", nil }
	glob = func(string) ([]string, error) { return nil, nil }
	t.Cleanup(restoreChromeHooks)
	_, err := FindChrome()
	if err == nil || err.Error() != "install chromium (sudo pacman -S chromium) or set KONSOLAI_CHROME" {
		t.Fatalf("%v", err)
	}
}

func restoreChromeHooks() {
	lookPath = exec.LookPath
	getenv = os.Getenv
	userHome = os.UserHomeDir
	glob = filepath.Glob
}
