package repl

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/SniksaX/Konsolai/internal/commands"
	"github.com/SniksaX/Konsolai/internal/config"
	"github.com/SniksaX/Konsolai/internal/ui/theme"
	"github.com/reeflective/readline"
)

const (
	slashNavHint = "↑/↓ · enter · esc"
	slashMenuCap = 8
)

func slashStyle() string {
	return ""
}

func Completer(reg *commands.Registry, cwdFn func() string) func(line []rune, cursor int) readline.Completions {
	return func(line []rune, cursor int) readline.Completions {
		if cursor < 0 {
			cursor = 0
		}
		if cursor > len(line) {
			cursor = len(line)
		}
		s := string(line[:cursor])
		if strings.HasPrefix(s, "/") {
			if !strings.Contains(s, " ") {
				return readline.Completions{}
			}
			return completeSlash(reg, s)
		}
		start := lastTokenStart(s)
		tok := s[start:]
		_ = cwdFn
		if strings.HasPrefix(tok, "@") {
			// The @ menu owns file completion. Tab must not cycle readline candidates.
			return readline.Completions{}
		}
		return readline.Completions{}
	}
}

func lastTokenStart(s string) int {
	i := len(s)
	for i > 0 {
		r, size := utf8.DecodeLastRuneInString(s[:i])
		if unicode.IsSpace(r) {
			return i
		}
		i -= size
	}
	return 0
}

func completeSlash(reg *commands.Registry, line string) readline.Completions {
	items := slashItems(reg, line)
	if len(items) == 0 {
		return readline.Completions{}
	}
	more := 0
	if len(items) > slashMenuCap {
		more = len(items) - slashMenuCap
		items = items[:slashMenuCap]
	}
	vals := make([]readline.Completion, 0, len(items)+1)
	room := theme.TermWidth() - 8
	if room < 12 {
		room = 12
	}
	for _, it := range items {
		disp := it.Display
		if disp == "" {
			disp = "  " + it.Value
		}
		desc := theme.Truncate(it.Desc, max(8, room-theme.DisplayWidth(disp)))
		vals = append(vals, readline.Completion{
			Value:       it.Value,
			Display:     disp,
			Description: desc,
			Style:       slashStyle(),
		})
	}
	if more > 0 {
		vals = append(vals, readline.Completion{
			Value:   line,
			Display: theme.Style(theme.Muted, fmt.Sprintf("↓ %d more", more)),
		})
	}
	comps := readline.CompleteRaw(vals)
	comps.PREFIX = slashPrefix(line)
	return comps.DisplayList().ListSeparator("  ").Usage(slashNavHint).NoSort()
}

type slashItem struct {
	Value   string
	Display string
	Desc    string
}

func slashItems(reg *commands.Registry, line string) []slashItem {
	if !strings.HasPrefix(line, "/") || reg == nil {
		return nil
	}
	rest := strings.TrimPrefix(line, "/")
	parts := strings.Fields(rest)
	space := strings.HasSuffix(line, " ")
	if !space && len(parts) <= 1 {
		var out []slashItem
		for _, c := range reg.All() {
			value, display, ok := matchCommand(c, line)
			if !ok {
				continue
			}
			out = append(out, slashItem{Value: value, Display: display, Desc: c.Summary})
		}
		return cap50items(out)
	}
	if len(parts) == 0 {
		return nil
	}
	cmd := parts[0]
	if cmd == "set" {
		return setSlashItems(parts, space)
	}
	arg := ""
	if !space && len(parts) > 1 {
		arg = parts[len(parts)-1]
	}
	var out []slashItem
	for _, o := range slashArgs(cmd) {
		if arg == "" || strings.HasPrefix(o.Value, arg) {
			if o.Display == "" {
				o.Display = "  " + o.Value
			}
			out = append(out, o)
		}
	}
	return cap50items(out)
}

func matchCommand(c commands.Command, line string) (value, display string, ok bool) {
	names := append([]string{c.Name}, c.Aliases...)
	shown := make([]string, 0, len(names))
	for _, n := range names {
		shown = append(shown, "/"+n)
	}
	for _, n := range names {
		full := "/" + n
		if strings.HasPrefix(full, line) {
			return full, "  " + strings.Join(shown, ", "), true
		}
	}
	return "", "", false
}

func setSlashItems(parts []string, space bool) []slashItem {
	partial := ""
	if !space && len(parts) > 1 {
		partial = parts[len(parts)-1]
	}
	vals := config.CompleteSet(parts[1:], partial, space && len(parts) > 1)
	out := make([]slashItem, 0, len(vals))
	for _, v := range vals {
		out = append(out, slashItem{Value: v, Display: "  " + v})
	}
	return out
}

func slashPrefix(line string) string {
	if !strings.HasPrefix(line, "/") {
		return line
	}
	rest := strings.TrimPrefix(line, "/")
	if !strings.Contains(rest, " ") && !strings.HasSuffix(line, " ") {
		return line
	}
	if strings.HasSuffix(line, " ") {
		return ""
	}
	i := strings.LastIndex(line, " ")
	if i < 0 {
		return line
	}
	return line[i+1:]
}

func slashArgs(cmd string) []slashItem {
	switch cmd {
	case "copy":
		return []slashItem{
			{Value: "answer", Desc: "Copy the last answer"},
			{Value: "all", Desc: "Copy the last prompt and answer"},
			{Value: "code", Desc: "Copy a code block from the last answer"},
			{Value: "cmd", Desc: "Copy a command from the last answer"},
		}
	case "think":
		return []slashItem{
			{Value: "on", Desc: "Always think"},
			{Value: "off", Desc: "Never think"},
			{Value: "auto", Desc: "Think when the intent asks for it"},
		}
	case "yes":
		return []slashItem{
			{Value: "on", Desc: "Auto-approve for this session"},
			{Value: "off", Desc: "Ask before mutating actions"},
		}
	case "trace":
		return []slashItem{
			{Value: "on", Desc: "Print per-call traces"},
			{Value: "off", Desc: "Hide per-call traces"},
		}
	default:
		return nil
	}
}

func cap50items(s []slashItem) []slashItem {
	if len(s) > 50 {
		return s[:50]
	}
	return s
}
