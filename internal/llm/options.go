package llm

import "context"

// ResponseOption adjusts a single /responses request.
type ResponseOption func(*request)

// WithContext attaches ctx to the request so the HTTP call can be cancelled or
// time-limited. Without it, Response uses context.Background.
func WithContext(ctx context.Context) ResponseOption {
	return func(r *request) {
		r.ctx = ctx
	}
}

// WithMaxOutputTokens caps the number of tokens the model may generate.
func WithMaxOutputTokens(n int) ResponseOption {
	return func(r *request) {
		r.MaxOutputTokens = &n
	}
}

// WithTemperature sets the sampling temperature.
func WithTemperature(temperature float64) ResponseOption {
	return func(r *request) {
		r.Temperature = &temperature
	}
}

// WithTools exposes one or more function tools to the model and lets the model
// decide when to call them (tool_choice "auto"). Calling it with no tools is a
// no-op.
func WithTools(tools ...Tool) ResponseOption {
	return func(r *request) {
		if len(tools) == 0 {
			return
		}
		for _, t := range tools {
			r.Tools = append(r.Tools, toolDefinition(t))
		}
		r.ToolChoice = "auto"
	}
}

// WithServerTools exposes one or more OpenRouter-run server tools to the
// model. Unlike WithTools it does not force tool_choice: the server tools run
// as part of the provider's normal response handling. Calling it with no tools
// is a no-op.
func WithServerTools(tools ...ServerTool) ResponseOption {
	return func(r *request) {
		for _, t := range tools {
			r.Tools = append(r.Tools, toolWire{Type: t.Type, Parameters: t.Parameters})
		}
	}
}

// WithInclude adds response includes (for example
// "reasoning.encrypted_content", required to receive and replay encrypted
// reasoning). Calling it with no values is a no-op.
func WithInclude(include ...string) ResponseOption {
	return func(r *request) {
		if len(include) == 0 {
			return
		}
		r.Include = append(r.Include, include...)
	}
}
