package research

import (
	"log/slog"

	"github.com/maxdollinger/meeth-councile/internal/llm"
)

// Option configures an Assistant.
type Option func(*Assistant)

// WithLogger sets the logger used to record research calls. A nil logger is
// ignored.
func WithLogger(logger *slog.Logger) Option {
	return func(a *Assistant) {
		if logger != nil {
			a.logger = logger
		}
	}
}

// WithResponseOptions passes additional options (temperature, include, max
// output tokens, ...) through on every model call.
func WithResponseOptions(opts ...llm.ResponseOption) Option {
	return func(a *Assistant) {
		a.opts = append(a.opts, opts...)
	}
}
