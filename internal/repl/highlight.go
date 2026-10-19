package repl

import (
	"strings"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/ui"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
)

// highlightInput colors the live line. A leading slash command is Accent,
// its arguments are Primary, and an unknown command word is Error.
// Submitted lines stay on echoBody.
func highlightInput(line string, reg *commands.Registry) string {
	if !strings.HasPrefix(line, "/") {
		return echoBody(line)
	}
	if !theme.Enabled() {
		return line
	}
	i := 1
	for i < len(line) && line[i] != ' ' {
		i++
	}
	cmd := line[:i]
	rest := line[i:]
	p := ui.Current()
	name := strings.TrimPrefix(cmd, "/")
	if name != "" && (reg == nil || reg.Lookup(name) == nil) {
		return p.Error(cmd) + theme.Style(theme.Primary, rest)
	}
	return p.Accent(cmd) + theme.Style(theme.Primary, rest)
}
