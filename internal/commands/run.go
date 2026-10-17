package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/siraj/konsolai/internal/repo"
	"github.com/siraj/konsolai/internal/safety"
	"github.com/siraj/konsolai/internal/store"
	"github.com/siraj/konsolai/internal/tools"
	"github.com/siraj/konsolai/internal/ui"
)

func runRun(ctx context.Context, env *Env, args []string) int {
	items := loadHistoryFixed(ctx, env, 1)
	if len(items) == 0 {
		fmt.Fprintln(env.errw(), "no answers")
		return 1
	}
	pairs := pairsFromMsgs(items[0].Msgs)
	if len(pairs) == 0 {
		fmt.Fprintln(env.errw(), "no answers")
		return 1
	}
	blocks := fenceBlocks(pairs[len(pairs)-1].Answer, "bash", "sh", "shell", "zsh")
	if len(blocks) == 0 {
		fmt.Fprintln(env.errw(), "no shell blocks")
		return 1
	}
	idx := 0
	if len(blocks) == 1 {
		idx = 0
	} else {
		if len(args) == 0 {
			for i, b := range blocks {
				fmt.Fprintf(env.out(), "%d  %s\n", i+1, clipTitle(b))
			}
			fmt.Fprintln(env.errw(), "usage: /run <k>")
			return 2
		}
		k, errMsg := parseN(args, 0, len(blocks))
		if errMsg != "" || k < 1 || k > len(blocks) {
			fmt.Fprintln(env.errw(), "usage: /run <k>")
			return 2
		}
		idx = k - 1
	}
	cmd := blocks[idx]
	fmt.Fprintln(env.out(), cmd)
	root := repo.Root(env.Cwd)
	g, err := safety.NewAt(root, env.Cwd)
	if err != nil {
		fmt.Fprintln(env.errw(), "konsolai:", err)
		return 1
	}
	outw := env.out()
	if !env.Plain {
		outw = ui.NewGutter(outw)
	}
	reg := tools.NewRegistry(g, outw, nil, nil, nil, tools.Options{
		ShellDefaultTimeout: env.Profile.ShellDefaultTimeout(),
		ShellMaxTimeout:     env.Profile.ShellMaxTimeout(),
		ShellWaitDelay:      env.Profile.ShellWaitDelay(),
	})
	tool, ok := reg.Get("run_shell")
	if !ok {
		return 1
	}
	raw, _ := json.Marshal(map[string]string{"cmd": cmd})
	act, err := tool.Prepare(string(raw))
	if err != nil {
		fmt.Fprintln(env.errw(), err)
		return 1
	}
	if act.Kind() != tools.ReadOnly && !env.Yes {
		conf := env.Confirm
		if conf == nil {
			p, err := ui.OpenPrompter(false)
			if err != nil {
				fmt.Fprintln(env.errw(), "konsolai:", err)
				return 1
			}
			defer p.Close()
			conf = p
		}
		d, _, err := conf.Confirm(act.Summary(), act.Preview())
		if err != nil {
			fmt.Fprintln(env.errw(), err)
			return 1
		}
		if d == ui.Quit {
			return 3
		}
		if d != ui.Approve && d != ui.ApproveAll {
			return 3
		}
	}
	start := time.Now()
	res, err := act.Run(ctx)
	elapsed := time.Since(start)
	if err != nil {
		fmt.Fprintln(env.errw(), err)
		return 1
	}
	mark := "✓"
	if res.IsError {
		mark = "✗"
	}
	fmt.Fprintf(env.out(), "%s %s · %s\n", mark, res.Meta, elapsed.Round(time.Millisecond))
	if sid := items[0].ID; sid != "" && env.Mem != nil && env.Mem.Store() != nil {
		s := env.Mem.Store()
		seq, err := s.NextSeq(ctx, sid)
		if err == nil {
			_ = s.RecordAction(ctx, store.ActionIn{
				SessionID: sid, Seq: seq, Tool: "run_shell", Args: raw,
				Kind: act.Kind().String(), Decision: "auto", IsError: res.IsError,
				Meta: res.Meta, Duration: elapsed, Output: res.Output,
			})
			_ = s.TouchSession(ctx, sid)
		}
	}
	if res.IsError {
		return 1
	}
	return 0
}
