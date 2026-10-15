package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/siraj/konsolai/internal/llm"
	"github.com/siraj/konsolai/internal/tools"
	"github.com/siraj/konsolai/internal/ui"
)

type Config struct {
	Client   *llm.Client
	Tools    *tools.Registry
	Prompter *ui.Prompter
	Out      io.Writer
	MaxSteps int
	Thinking bool
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
				d, reason, err := cfg.Prompter.Confirm(action.Summary(), action.Preview())
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
			fmt.Fprintln(cfg.Out, ui.Cyan("▸ ")+action.Summary())
			result, err := action.Run(ctx)
			if err != nil {
				return err
			}
			if result.IsError {
				fmt.Fprintln(cfg.Out, ui.Red("✗"))
			} else {
				fmt.Fprintln(cfg.Out, ui.Green("✓"))
			}
			msgs = append(msgs, llm.Message{
				Role:       llm.RoleTool,
				Content:    result.Output,
				ToolCallID: call.ID,
			})
			if result.Final {
				fmt.Fprintln(cfg.Out, ui.Bold(result.Output))
				return nil
			}
		}
	}
	return ErrMaxSteps
}
