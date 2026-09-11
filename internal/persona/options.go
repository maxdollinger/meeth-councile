package persona

import (
	"log/slog"

	"github.com/maxdollinger/meeth-councile/internal/llm"
)

// Option configures a Persona.
type Option func(*Persona)

// WithLogger sets the logger used to record the persona's model calls. The
// persona name is attached to every record. A nil logger is ignored.
func WithLogger(logger *slog.Logger) Option {
	return func(p *Persona) {
		if logger != nil {
			p.logger = logger.With("persona", p.name)
		}
	}
}

// WithMaxSteps caps how many model calls the tool loop may make. Values below 1
// are ignored.
func WithMaxSteps(n int) Option {
	return func(p *Persona) {
		if n >= 1 {
			p.maxSteps = n
		}
	}
}

// WithResponseOptions passes additional options (temperature, include, max
// output tokens, ...) through on every model call, both the comprehension call
// and the tool loop.
func WithResponseOptions(opts ...llm.ResponseOption) Option {
	return func(p *Persona) {
		p.opts = append(p.opts, opts...)
	}
}
