package commands

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestReadmeImagesExist checks the images the README itself ships.
//
// Badge images point at http(s) URLs and are owned by their providers, so
// they are skipped: only relative paths have to exist in this checkout. The
// centered header uses <img>, so both Markdown and HTML images are collected.
func TestReadmeImagesExist(t *testing.T) {
	text := readReadme(t)
	markdown := regexp.MustCompile(`!\[[^\]]*\]\(([^)\s]+)\)`).FindAllStringSubmatch(text, -1)
	html := regexp.MustCompile(`<img[^>]*\bsrc="([^"]+)"`).FindAllStringSubmatch(text, -1)
	if len(markdown)+len(html) == 0 {
		t.Fatal("readme has no images")
	}
	root := repoRoot(t)
	for _, match := range append(markdown, html...) {
		ref := match[1]
		if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
			// Badge or remote asset: not part of the checkout.
			continue
		}
		if strings.Contains(ref, "://") || strings.HasPrefix(ref, "/") {
			t.Errorf("image path %q is not relative", ref)
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(ref))
		if _, err := os.Stat(abs); err != nil {
			t.Errorf("image %s: %v", ref, err)
		}
	}
}

func TestReadmeOmitsSampleHost(t *testing.T) {
	text := readReadme(t)
	if strings.Contains(text, "gpu-box") {
		t.Fatal("readme contains gpu-box")
	}
}

func readReadme(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func readDoc(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
