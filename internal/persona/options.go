package persona

import "github.com/maxdollinger/meeth-councile/internal/llm"

// Option configures a Persona.
type Option func(*Persona)

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
