package docrender

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

var chromeBins = []string{
	"chromium",
	"chromium-browser",
	"google-chrome-stable",
	"google-chrome",
	"brave",
	"brave-browser",
}

var (
	lookPath = exec.LookPath
	getenv   = os.Getenv
	userHome = os.UserHomeDir
	glob     = filepath.Glob
)

func FindChrome() (string, error) {
	if p := strings.TrimSpace(getenv("KONSOLAI_CHROME")); p != "" {
		return p, nil
	}
	var found string
	for _, name := range chromeBins {
		p, err := lookPath(name)
		if err == nil && p != "" {
			found = p
			break
		}
	}
	if pup := newestPuppeteerChrome(); pup != "" {
		if found == "" || isBrave(found) {
			return pup, nil
		}
	}
	if found != "" {
		return found, nil
	}
	return "", errors.New("install chromium (sudo pacman -S chromium) or set KONSOLAI_CHROME")
}

func isBrave(p string) bool {
	b := strings.ToLower(filepath.Base(p))
	return b == "brave" || b == "brave-browser"
}

func newestPuppeteerChrome() string {
	home, err := userHome()
	if err != nil {
		return ""
	}
	matches, err := glob(filepath.Join(home, ".cache", "puppeteer", "chrome", "linux-*", "chrome-linux64", "chrome"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[len(matches)-1]
}
