package commands

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/SniksaX/Konsolai/internal/redact"
	"github.com/SniksaX/Konsolai/internal/turn"
)

// explainCap bounds the failed-command output placed in the answer prompt.
const explainCap = 4000

func runExplain(ctx context.Context, env *Env, args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(env.errw(), "usage: /explain")
		return 2
	}
	if env != nil && env.FailedShell != nil {
		cmd, code, output, ok := env.FailedShell()
		if ok && strings.TrimSpace(cmd) != "" {
			return explainTurn(ctx, env, explainCaptured(cmd, code, output))
		}
	}
	line, ok := lastHistLine()
	if !ok {
		fmt.Fprintln(env.out(), "no failed command to explain")
		return 1
	}
	fmt.Fprintln(env.out(), "no captured output; explaining the command only")
	return explainTurn(ctx, env, explainHist(line))
}

func explainCaptured(cmd string, exit int, output string) string {
	output = redact.String(output)
	output = clipExplain(output)
	return fmt.Sprintf("Command: %s\nExit code: %d\nOutput:\n%s\n\nExplain why it failed and give the fix.", cmd, exit, output)
}

func explainHist(cmd string) string {
	return "no captured output; explaining the command only\n\nCommand: " + cmd + "\n\nExplain why it failed and give the fix."
}

func explainTurn(ctx context.Context, env *Env, text string) int {
	req := turn.Request{
		Text: text, Intent: "answer", Effort: "high", Yes: env.Yes, Plain: env.Plain, Cwd: env.Cwd,
	}
	if env.RunTurn != nil {
		return env.RunTurn(ctx, req).ExitCode
	}
	if env.Runner == nil {
		fmt.Fprintln(env.errw(), "konsolai: runner not available")
		return 1
	}
	return env.Runner.RunTurn(ctx, req).ExitCode
}

func lastHistLine() (string, bool) {
	path := os.Getenv("HISTFILE")
	if path == "" {
		return "", false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	lines := strings.Split(string(b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line, true
		}
	}
	return "", false
}

func clipExplain(s string) string {
	if len(s) <= explainCap {
		return s
	}
	return s[:explainCap]
}
