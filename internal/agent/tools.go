package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/maxdollinger/meeth-councile/internal/llm"
)

// ExecuteFunc runs a tool call and returns its output as a string. arguments is
// the raw JSON argument object produced by the model; decode it with
// json.Unmarshal (or ToolCall.Args) as needed.
type ExecuteFunc func(ctx context.Context, arguments string) (string, error)

// Tool pairs an llm tool definition with the function that executes it. The
// definition is embedded, so Tool itself satisfies llm.Tool: Name,
// Description, and Parameters are promoted from the definition.
type Tool struct {
	llm.Tool
	Execute ExecuteFunc
}

// NewTool builds an executable tool from a function definition.
func NewTool(name, description string, parameters map[string]any, execute ExecuteFunc) Tool {
	return Tool{
		Tool:    llm.NewFunctionTool(name, description, parameters),
		Execute: execute,
	}
}

// execute runs one tool call. Any failure is returned to the model as a JSON
// error string rather than aborting the run, so the model can react to it.
func (a *Agent) execute(ctx context.Context, call llm.ToolCall) string {
	tool, ok := a.tools[call.Name]
	if !ok {
		return errorOutput(fmt.Sprintf("unknown tool: %s", call.Name))
	}
	if tool.Execute == nil {
		return errorOutput(fmt.Sprintf("tool %s has no executor", call.Name))
	}
	out, err := tool.Execute(ctx, call.Arguments)
	if err != nil {
		return errorOutput(err.Error())
	}
	return out
}

func errorOutput(msg string) string {
	raw, err := json.Marshal(map[string]string{"error": msg})
	if err != nil {
		return `{"error":"tool execution failed"}`
	}
	return string(raw)
}

func callID(call llm.ToolCall) string {
	if call.CallID != "" {
		return call.CallID
	}
	return call.ID
}
