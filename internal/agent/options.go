package agent

import (
	"log/slog"

	"github.com/maxdollinger/meeth-councile/internal/llm"
)

// Option configures an Agent.
type Option func(*Agent)

// WithLogger sets the logger used to record the agent's steps. A nil logger is
// ignored.
func WithLogger(logger *slog.Logger) Option {
	return func(a *Agent) {
		if logger != nil {
			a.logger = logger
		}
	}
}

// WithTools makes tools available to the agent.
func WithTools(tools ...Tool) Option {
	return func(a *Agent) {
		for _, t := range tools {
			a.tools[t.Name()] = t
			a.defs = append(a.defs, t.Tool)
		}
	}
}

// WithMaxSteps caps how many model calls Run may make. Values below 1 are
// ignored.
func WithMaxSteps(n int) Option {
	return func(a *Agent) {
		if n >= 1 {
			a.maxSteps = n
		}
	}
}

// WithResponseOptions passes additional options (temperature, include, max
// output tokens, ...) through on every model call.
func WithResponseOptions(opts ...llm.ResponseOption) Option {
	return func(a *Agent) {
		a.opts = append(a.opts, opts...)
	}
}
