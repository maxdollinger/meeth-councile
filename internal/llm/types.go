package llm

import (
	"encoding/json"
	"fmt"
)

// APIError is returned for any non-success response from OpenRouter, whether
// signalled by an HTTP status or an error object in the body.
type APIError struct {
	Status  int
	Code    int
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("llm: unexpected status %d", e.Status)
	}
	code := e.Code
	if code == 0 {
		code = e.Status
	}
	return fmt.Sprintf("llm: %d: %s", code, e.Message)
}

type Usage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	TotalTokens  int     `json:"total_tokens"`
	Cost         float64 `json:"cost"`
}

// ToolCall is a function call the model asked to make. Arguments is the raw
// JSON string the model produced; use Args to decode it.
type ToolCall struct {
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Args decodes the call's JSON argument string into v.
func (tc ToolCall) Args(v any) error {
	return json.Unmarshal([]byte(tc.Arguments), v)
}

type Result struct {
	Text      string      `json:"text"`
	ID        string      `json:"id"`
	Model     string      `json:"model"`
	Usage     Usage       `json:"usage"`
	ToolCalls []ToolCall  `json:"tool_calls,omitempty"`
	Reasoning []Reasoning `json:"reasoning,omitempty"`
	Output    []Item      `json:"-"`
}

type request struct {
	Model           string           `json:"model"`
	Input           []map[string]any `json:"input"`
	MaxOutputTokens *int             `json:"max_output_tokens,omitempty"`
	Temperature     *float64         `json:"temperature,omitempty"`
	Tools           []toolWire       `json:"tools,omitempty"`
	ToolChoice      string           `json:"tool_choice,omitempty"`
	Include         []string         `json:"include,omitempty"`
}

type rawResponse struct {
	ID     string       `json:"id"`
	Model  string       `json:"model"`
	Output []outputItem `json:"output"`
	Usage  *Usage       `json:"usage"`
	Error  *apiError    `json:"error"`
}

type outputItem struct {
	Type             string        `json:"type"`
	ID               string        `json:"id"`
	CallID           string        `json:"call_id"`
	Name             string        `json:"name"`
	Arguments        string        `json:"arguments"`
	Content          []contentPart `json:"content"`
	Summary          []summaryPart `json:"summary"`
	EncryptedContent string        `json:"encrypted_content"`
	Format           string        `json:"format"`
	Signature        string        `json:"signature"`
	Status           string        `json:"status"`
}

type contentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type summaryPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
