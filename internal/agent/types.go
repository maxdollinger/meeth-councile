package agent

import "github.com/maxdollinger/meeth-councile/internal/llm"

// Result is the outcome of a completed Run.
type Result struct {
	Text   string
	Passed bool
	Steps  int
	Usage  llm.Usage

	// History is the complete loop: the input plus every model output item
	// (reasoning, messages, tool calls) and every tool result, in order. The
	// agent's system prompt is not included, since Run re-adds it; passing
	// History back to a later Run resumes the conversation with reasoning and
	// tool calls intact.
	History llm.Input
}
