package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/SniksaX/Konsolai/internal/config"
)

// TestReadmeSlashTable keeps the README slash table aligned with Default().
// Columns are DisplayNames, formatted Args, and Summary, in registry order.
// A required Arg is its name. An optional Arg is [name].
func TestReadmeFriendlyTable(t *testing.T) {
	text := readReadme(t)
	if !strings.Contains(text, "| name | values | keys |") {
		t.Fatal("readme missing friendly table")
	}
	for _, line := range strings.Split(strings.TrimSpace(config.FriendlyTable()), "\n") {
		fields := strings.Split(line, "|")
		if len(fields) < 3 {
			continue
		}
		name := strings.TrimSpace(fields[1])
		if name == "" || name == "name" || name == "---" {
			continue
		}
		if !strings.Contains(text, "| "+name+" |") {
			t.Errorf("readme missing friendly name %s", name)
		}
	}
}

func TestReadmeSlashTable(t *testing.T) {
	text := readReadme(t)
	got, err := slashRows(text)
	if err != nil {
		t.Fatal(err)
	}
	want := registrySlashRows()
	if len(got) != len(want) {
		t.Fatalf("slash table has %d rows, registry has %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d\n got name=%q args=%q description=%q\nwant name=%q args=%q description=%q",
				i, got[i][0], got[i][1], got[i][2], want[i][0], want[i][1], want[i][2])
		}
	}
}

func TestReadmeImagesExist(t *testing.T) {
	text := readReadme(t)
	if strings.Contains(text, "<img") || strings.Contains(strings.ToLower(text), "<image") {
		t.Fatal("readme uses an HTML image")
	}
	root := repoRoot(t)
	re := regexp.MustCompile(`!\[[^\]]*\]\(([^)\s]+)\)`)
	matches := re.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		t.Fatal("readme has no images")
	}
	for _, m := range matches {
		p := m[1]
		if strings.Contains(p, "://") || strings.HasPrefix(p, "/") {
			t.Errorf("image path %q is not relative", p)
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(p))
		if _, err := os.Stat(abs); err != nil {
			t.Errorf("image %s: %v", p, err)
		}
	}
}

func TestReadmeOmitsSampleHost(t *testing.T) {
	text := readReadme(t)
	if strings.Contains(text, "gpu-box") {
		t.Fatal("readme contains gpu-box")
	}
}

func TestReadmeEnvOverrides(t *testing.T) {
	text := readReadme(t)
	if strings.Contains(text, ".env.example") || strings.Contains(text, "config.default.yaml") {
		t.Fatal("readme names a removed config file")
	}
	section := readmeSection(text, "### Environment overrides")
	if section == "" {
		t.Fatal("readme missing Environment overrides")
	}
	note := "Prefer `config.yaml`. Environment variables are for containers and CI."
	if !strings.Contains(section, note) {
		t.Fatal("readme missing the environment-override note")
	}
	if !strings.Contains(section, config.EnvOverrideTable()) {
		t.Fatalf("config env table drifted\nwant:\n%s", config.EnvOverrideTable())
	}
	if !strings.Contains(section, config.ComposeVarTable()) {
		t.Fatalf("compose var table drifted\nwant:\n%s", config.ComposeVarTable())
	}
	for _, k := range config.Keys() {
		if k.Env == "" {
			continue
		}
		if !strings.Contains(section, k.Env) || !strings.Contains(section, k.Path) {
			t.Errorf("section missing %s (%s)", k.Env, k.Path)
		}
	}
}

func readmeSection(text, heading string) string {
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if line == heading {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") && !strings.HasPrefix(lines[i], "###") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

func registrySlashRows() [][3]string {
	cmds := Default().All()
	rows := make([][3]string, 0, len(cmds))
	for _, c := range cmds {
		rows = append(rows, [3]string{c.DisplayNames(), formatReadmeArgs(c), c.Summary})
	}
	return rows
}

func formatReadmeArgs(c Command) string {
	if len(c.Args) == 0 {
		return ""
	}
	parts := make([]string, len(c.Args))
	for i, a := range c.Args {
		if a.Required {
			parts[i] = a.Name
		} else {
			parts[i] = "[" + a.Name + "]"
		}
	}
	return strings.Join(parts, " ")
}

func slashRows(text string) ([][3]string, error) {
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "## Slash commands" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("readme: missing ## Slash commands")
	}
	var table []string
	seen := false
	for _, line := range lines[start:] {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "## ") {
			break
		}
		if strings.HasPrefix(trim, "|") {
			seen = true
			table = append(table, trim)
			continue
		}
		if seen {
			break
		}
	}
	if len(table) < 3 {
		return nil, fmt.Errorf("readme: slash table missing")
	}
	header, err := splitTableRow(table[0])
	if err != nil {
		return nil, err
	}
	if header[0] != "name" || header[1] != "args" || header[2] != "description" {
		return nil, fmt.Errorf("readme: slash header = %q", header)
	}
	sep, err := splitTableRow(table[1])
	if err != nil {
		return nil, err
	}
	if !separatorRow(sep) {
		return nil, fmt.Errorf("readme: slash separator = %q", sep)
	}
	rows := make([][3]string, 0, len(table)-2)
	for _, line := range table[2:] {
		cells, err := splitTableRow(line)
		if err != nil {
			return nil, err
		}
		rows = append(rows, [3]string{cells[0], cells[1], cells[2]})
	}
	return rows, nil
}

func splitTableRow(line string) ([]string, error) {
	if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
		return nil, fmt.Errorf("readme: bad table row %q", line)
	}
	parts := strings.Split(line[1:len(line)-1], "|")
	if len(parts) != 3 {
		return nil, fmt.Errorf("readme: want 3 columns, got %d in %q", len(parts), line)
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts, nil
}

func separatorRow(cells []string) bool {
	for _, c := range cells {
		if c == "" {
			return false
		}
		for _, r := range c {
			if r != '-' && r != ':' {
				return false
			}
		}
	}
	return true
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
