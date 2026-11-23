package commands

import (
	"fmt"
	"strings"
	"testing"
)

// TestDocsSlashTable keeps docs/commands.md aligned with the registry.
// Columns are DisplayNames, formatted Args, and Summary, in registry order.
func TestDocsSlashTable(t *testing.T) {
	text := readDoc(t, "docs/commands.md")
	got, err := docsSlashRows(text)
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

func docsSlashRows(text string) ([][3]string, error) {
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "## Slash commands" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("commands doc: missing ## Slash commands")
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
		return nil, fmt.Errorf("commands doc: slash table missing")
	}
	header, err := splitTableRow(table[0])
	if err != nil {
		return nil, err
	}
	if header[0] != "name" || header[1] != "args" || header[2] != "description" {
		return nil, fmt.Errorf("commands doc: slash header = %q", header)
	}
	sep, err := splitTableRow(table[1])
	if err != nil {
		return nil, err
	}
	if !separatorRow(sep) {
		return nil, fmt.Errorf("commands doc: slash separator = %q", sep)
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
		return nil, fmt.Errorf("commands doc: bad table row %q", line)
	}
	parts := strings.Split(line[1:len(line)-1], "|")
	if len(parts) != 3 {
		return nil, fmt.Errorf("commands doc: want 3 columns, got %d in %q", len(parts), line)
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
