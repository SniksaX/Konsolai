package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/tools"
	"github.com/siraj/konsolai/internal/ui"
)

type Confirmer interface {
	Confirm(summary, preview string) (ui.Decision, string, error)
}

type Config struct {
	Client    *llm.Client
	Tools     *tools.Registry
	Confirmer Confirmer
	Out       io.Writer
	MaxSteps  int
	Thinking  bool
}

var (
	ErrAborted  = errors.New("agent: aborted")
	ErrMaxSteps = errors.New("agent: max steps exceeded")
)

func Run(ctx context.Context, cfg Config, systemContext, task string) error {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt + "\n\n" + systemContext},
		{Role: llm.RoleUser, Content: task},
	}
	opts := llm.ChatOptions{Thinking: cfg.Thinking, Temperature: 0.2}
	specs := cfg.Tools.Specs()
	seenFail := make(map[string]struct{})

	for step := 0; step < cfg.MaxSteps; step++ {
		clear := ui.Status(cfg.Out, "planning...")
		msg, err := cfg.Client.Complete(ctx, msgs, specs, opts)
		clear()
		if err != nil {
			return err
		}
		msgs = append(msgs, msg)

		if len(msg.ToolCalls) == 0 {
			if text := strings.TrimSpace(msg.Content); text != "" {
				fmt.Fprintln(cfg.Out, text)
			}
			return nil
		}
		if text := strings.TrimSpace(msg.Content); text != "" {
			fmt.Fprintln(cfg.Out, ui.Dim(text))
		}

		for _, call := range msg.ToolCalls {
			if err := ctx.Err(); err != nil {
				return err
			}
			tool, ok := cfg.Tools.Get(call.Function.Name)
			if !ok {
				msgs = append(msgs, llm.Message{
					Role:       llm.RoleTool,
					Content:    "error: unknown tool " + call.Function.Name,
					ToolCallID: call.ID,
				})
				continue
			}
			action, err := tool.Prepare(call.Function.Arguments)
			if err != nil {
				msgs = append(msgs, llm.Message{
					Role:       llm.RoleTool,
					Content:    "error: " + err.Error(),
					ToolCallID: call.ID,
				})
				continue
			}
			if !action.ReadOnly() {
				d, reason, err := cfg.Confirmer.Confirm(action.Summary(), action.Preview())
				if err != nil {
					return err
				}
				switch d {
				case ui.Reject:
					msgs = append(msgs, llm.Message{
						Role:       llm.RoleTool,
						Content:    "user rejected: " + reason,
						ToolCallID: call.ID,
					})
					continue
				case ui.Quit:
					return ErrAborted
				}
			}
			finish := call.Function.Name == "finish"
			if !finish {
				fmt.Fprintln(cfg.Out, ui.Cyan("▸ ")+action.Summary())
			}
			start := time.Now()
			result, err := action.Run(ctx)
			elapsed := time.Since(start)
			if err != nil {
				return err
			}
			if result.Final {
				fmt.Fprintln(cfg.Out)
				fmt.Fprintln(cfg.Out, ui.Bold(result.Output))
				return nil
			}
			mark := ui.Green("✓")
			if result.IsError {
				mark = ui.Red("✗")
			}
			fmt.Fprintf(cfg.Out, "%s %s · %s\n", mark, result.Meta, formatDuration(elapsed))
			content := result.Output
			if result.IsError {
				sum := sha256.Sum256([]byte(result.Output))
				key := call.Function.Name + call.Function.Arguments + string(sum[:])
				if _, dup := seenFail[key]; dup {
					content += "\n\nnote: this exact call already failed with identical output. Do not retry it; inspect files or change approach."
				} else {
					seenFail[key] = struct{}{}
				}
			}
			msgs = append(msgs, llm.Message{
				Role:       llm.RoleTool,
				Content:    content,
				ToolCallID: call.ID,
			})
		}
	}
	return ErrMaxSteps
}

func formatDuration(d time.Duration) string {
	return fmt.Sprintf("%.1fs", d.Seconds())
}
