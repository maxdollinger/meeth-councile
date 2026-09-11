package llm

// ResponseOption adjusts a single /responses request.
type ResponseOption func(*request)

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
