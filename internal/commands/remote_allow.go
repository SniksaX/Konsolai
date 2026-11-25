package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/SniksaX/Konsolai/internal/config"
)

// runRemote resets the process-wide remote confirmation decision, so a
// declined host can be accepted again without restarting konsolai.
func runRemote(_ context.Context, env *Env, args []string) int {
	if len(args) != 1 || !strings.EqualFold(strings.TrimSpace(args[0]), "allow") {
		fmt.Fprintln(env.errw(), "usage: /remote allow")
		return 2
	}
	config.ResetRemoteState()
	fmt.Fprintln(env.out(), "remote prompts are enabled again for this process")
	return 0
}
