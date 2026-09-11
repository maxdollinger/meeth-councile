// Package agent runs a single conversational agent on top of the llm client:
// it sends a system prompt plus input, executes any tool calls the model asks
// for, feeds the results back, and repeats until the model answers without
// calling a tool (or the step cap is hit).
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/maxdollinger/meeth-councile/internal/llm"
)

const defaultMaxSteps = 8

// CompletionClient is the slice of *llm.Client the agent depends on. It exists
// so tests can substitute a scripted responder.
type CompletionClient interface {
	Response(model string, input llm.Input, opts ...llm.ResponseOption) (llm.Result, error)
}

// Agent is a single conversational agent.
type Agent struct {
	client   CompletionClient
	model    string
	system   string
	tools    map[string]Tool
	defs     []llm.Tool
	maxSteps int
	opts     []llm.ResponseOption
}

// New builds an Agent with the given system prompt. Registered tools are
// available for the model to call.
func New(client CompletionClient, model, system string, opts ...Option) *Agent {
	a := &Agent{
		client:   client,
		model:    model,
		system:   system,
		tools:    make(map[string]Tool),
		maxSteps: defaultMaxSteps,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Run sends input and loops on tool calls until the model produces a final
// answer or maxSteps model calls have been made.
func (a *Agent) Run(ctx context.Context, input llm.Input) (Result, error) {
	conv := make(llm.Input, 0, len(input)+1)
	if a.system != "" {
		conv = append(conv, llm.System(a.system))
	}
	conv = append(conv, input...)

	history := append(llm.Input(nil), input...)

	var total llm.Usage
	for step := 1; step <= a.maxSteps; step++ {
		res, err := a.client.Response(a.model, conv, a.callOptions(ctx)...)
		if err != nil {
			return Result{Steps: step - 1, Usage: total, History: history}, err
		}
		total.InputTokens += res.Usage.InputTokens
		total.OutputTokens += res.Usage.OutputTokens
		total.TotalTokens += res.Usage.TotalTokens
		total.Cost += res.Usage.Cost

		items := res.Items()
		history = append(history, items...)

		if len(res.ToolCalls) == 0 {
			return Result{
				Text:    res.Text,
				Passed:  IsPass(res.Text),
				Steps:   step,
				Usage:   total,
				History: history,
			}, nil
		}

		conv = append(conv, items...)
		for _, call := range res.ToolCalls {
			output := llm.FunctionCallOutput{
				CallID: callID(call),
				Output: a.execute(ctx, call),
			}
			conv = append(conv, output)
			history = append(history, output)
		}
	}
	return Result{Steps: a.maxSteps, Usage: total, History: history},
		fmt.Errorf("agent: exceeded max steps (%d)", a.maxSteps)
}

func (a *Agent) callOptions(ctx context.Context) []llm.ResponseOption {
	opts := make([]llm.ResponseOption, 0, len(a.opts)+2)
	opts = append(opts, a.opts...)
	if len(a.defs) > 0 {
		opts = append(opts, llm.WithTools(a.defs...))
	}
	return append(opts, llm.WithContext(ctx))
}

// IsPass reports whether text is the exact PASS sentinel (surrounding
// whitespace ignored).
func IsPass(text string) bool {
	return strings.TrimSpace(text) == "PASS"
}
