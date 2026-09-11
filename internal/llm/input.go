package llm

// Role identifies the author of a Message input item.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Item is one entry in the /responses `input` array. Concrete conversation
// types participate by implementing ResponseItem; the built-in types are
// Message, FunctionCall, and FunctionCallOutput.
type Item interface {
	// ResponseItem returns the JSON wire object for this input item.
	ResponseItem() map[string]any
}

// Input is an ordered conversation sent as the request `input`. Callers
// assemble it as needed and append Result.Items and FunctionCallOutput values
// between turns to close an agent/tool loop.
type Input []Item

// wire flattens the slice into request-ready maps, skipping nil entries.
func (in Input) wire() []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, item := range in {
		if item == nil {
			continue
		}
		out = append(out, item.ResponseItem())
	}
	return out
}

// Message is a chat message with a role and text content.
type Message struct {
	Role    Role
	Content string
}

func (m Message) ResponseItem() map[string]any {
	return map[string]any{
		"type":    "message",
		"role":    string(m.Role),
		"content": m.Content,
	}
}

// FunctionCall echoes a model tool call back into the conversation. ID is
// optional; CallID and Arguments are required by the API.
type FunctionCall struct {
	ID        string
	CallID    string
	Name      string
	Arguments string
}

func (f FunctionCall) ResponseItem() map[string]any {
	item := map[string]any{
		"type":      "function_call",
		"call_id":   f.CallID,
		"name":      f.Name,
		"arguments": f.Arguments,
	}
	if f.ID != "" {
		item["id"] = f.ID
	}
	return item
}

// FunctionCallOutput carries the result of executing a tool call. CallID must
// match the originating FunctionCall.
type FunctionCallOutput struct {
	CallID string
	Output string
}

func (f FunctionCallOutput) ResponseItem() map[string]any {
	return map[string]any{
		"type":    "function_call_output",
		"call_id": f.CallID,
		"output":  f.Output,
	}
}

// ReasoningSummary is a short summary of a reasoning item.
type ReasoningSummary struct {
	Text string
}

// ReasoningText is a reasoning content block.
type ReasoningText struct {
	Text string
}

// Reasoning is a model reasoning item echoed back into the conversation.
// Reasoning models need their reasoning replayed, in order, ahead of the
// function calls it produced. EncryptedContent is only populated when the
// request opts in via WithInclude("reasoning.encrypted_content").
type Reasoning struct {
	ID               string
	Summary          []ReasoningSummary
	Content          []ReasoningText
	EncryptedContent string
	Format           string
	Signature        string
}

func (r Reasoning) ResponseItem() map[string]any {
	summary := make([]map[string]any, 0, len(r.Summary))
	for _, s := range r.Summary {
		summary = append(summary, map[string]any{"type": "summary_text", "text": s.Text})
	}

	item := map[string]any{
		"type":    "reasoning",
		"id":      r.ID,
		"summary": summary,
	}
	if r.EncryptedContent != "" {
		item["encrypted_content"] = r.EncryptedContent
	}
	if len(r.Content) > 0 {
		content := make([]map[string]any, 0, len(r.Content))
		for _, c := range r.Content {
			content = append(content, map[string]any{"type": "reasoning_text", "text": c.Text})
		}
		item["content"] = content
	}
	if r.Format != "" {
		item["format"] = r.Format
	}
	if r.Signature != "" {
		item["signature"] = r.Signature
	}
	return item
}

// User builds a user message.
func User(text string) Message {
	return Message{Role: RoleUser, Content: text}
}

// System builds a system message.
func System(text string) Message {
	return Message{Role: RoleSystem, Content: text}
}

// Assistant builds an assistant message.
func Assistant(text string) Message {
	return Message{Role: RoleAssistant, Content: text}
}

// Items reconstructs the assistant turn so it can be appended to the next
// request. When the Result came from a parsed API response, Output preserves
// the exact item order (reasoning, message, function calls). A hand-built
// Result falls back to an assistant Message when Text is non-empty followed by
// one FunctionCall per ToolCall; a call's CallID falls back to its ID.
func (r Result) Items() Input {
	if len(r.Output) > 0 {
		return Input(r.Output)
	}

	var in Input
	if r.Text != "" {
		in = append(in, Assistant(r.Text))
	}
	for _, call := range r.ToolCalls {
		callID := call.CallID
		if callID == "" {
			callID = call.ID
		}
		in = append(in, FunctionCall{
			ID:        call.ID,
			CallID:    callID,
			Name:      call.Name,
			Arguments: call.Arguments,
		})
	}
	return in
}
