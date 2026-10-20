package turn

import (
	"encoding/json"
	"strings"

	"github.com/SniksaX/Konsolai/internal/intent"
	"github.com/SniksaX/Konsolai/internal/llm"
)

// EscalateTool is the only tool available in answer mode on the main model.
var EscalateTool = llm.ToolSpec{
	Type: "function",
	Function: llm.FunctionSpec{
		Name:        "escalate",
		Description: "Hand this turn to a mode that can use tools. Call when the user wants anything checked, run, tested, created, changed, started, stopped or killed.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"intent":{"type":"string","enum":["lookup","act","build"],"description":"lookup to inspect; act to run or change the machine; build to change code"},"reason":{"type":"string","description":"why tools are required"}},"required":["intent","reason"],"additionalProperties":false}`),
	},
}

func parseEscalate(calls []llm.ToolCall) intent.Intent {
	for _, c := range calls {
		if c.Function.Name != "escalate" {
			continue
		}
		var args struct {
			Intent string `json:"intent"`
		}
		_ = json.Unmarshal([]byte(c.Function.Arguments), &args)
		switch intent.Intent(strings.ToLower(strings.TrimSpace(args.Intent))) {
		case intent.Lookup, intent.Act, intent.Build:
			return intent.Intent(strings.ToLower(strings.TrimSpace(args.Intent)))
		}
	}
	return ""
}
