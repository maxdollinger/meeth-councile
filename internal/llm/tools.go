package llm

// Tool is a function exposed to the model. Implementations supply the
// definition sent to OpenRouter (see NewFunctionTool for the simple case);
// keeping an executable function on the same value, alongside these methods, is
// the intended extension point for a future tool loop.
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
}

// toolWire is the JSON shape of a tool in a /responses request. Function tools
// populate Name/Description; server tools (see ServerTool) leave them empty and
// are identified by Type alone.
type toolWire struct {
	Type        string         `json:"type"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ServerTool is a tool executed by OpenRouter rather than the caller. Unlike a
// function Tool it has no client-side executor: the wire type (for example
// "openrouter:web_search" or "openrouter:web_fetch") is enough, and the server
// runs the tool loop, returning the results in the model's final reply.
// Parameters, when set, follow that tool's own schema.
type ServerTool struct {
	Type       string
	Parameters map[string]any
}

func toolDefinition(t Tool) toolWire {
	return toolWire{
		Type:        "function",
		Name:        t.Name(),
		Description: t.Description(),
		Parameters:  t.Parameters(),
	}
}

// functionTool is the built-in Tool implementation for simple definitions.
type functionTool struct {
	name        string
	description string
	parameters  map[string]any
}

func (t functionTool) Name() string               { return t.name }
func (t functionTool) Description() string        { return t.description }
func (t functionTool) Parameters() map[string]any { return t.parameters }

// NewFunctionTool builds a Tool from a name, description, and JSON Schema
// parameters object.
func NewFunctionTool(name, description string, parameters map[string]any) Tool {
	return functionTool{name: name, description: description, parameters: parameters}
}
