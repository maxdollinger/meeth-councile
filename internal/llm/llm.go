package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	baseURL          = "https://openrouter.ai/api/v1"
	maxResponseBytes = 10 << 20
	httpReferer      = "https://github.com/maxdollinger/meeth-councile"
	appTitle         = "meeth-councile"
)

type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

func New(apiKey string, httpClient *http.Client) *Client {
	if apiKey == "" {
		panic("llm: api key is required")
	}
	if httpClient == nil {
		panic("llm: http client is required")
	}
	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		http:    httpClient,
	}
}

func (c *Client) Response(model string, input Input, opts ...ResponseOption) (Result, error) {
	wired := input.wire()
	if len(wired) == 0 {
		return Result{}, errors.New("llm: input is required")
	}
	req := request{Model: model, Input: wired}
	for _, opt := range opts {
		opt(&req)
	}

	raw, err := c.do(req)
	if err != nil {
		return Result{}, err
	}

	var parsed rawResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Result{}, fmt.Errorf("llm: decode response: %w", err)
	}
	return parsed.result()
}

// do sends body to the /responses endpoint and returns the raw response body.
// It owns request construction, headers, transport, and HTTP-status errors so
// that every OpenRouter call site shares one implementation.
func (c *Client) do(body any) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("llm: marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/responses", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("llm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("HTTP-Referer", httpReferer)
	req.Header.Set("X-Title", appTitle)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm: send request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("llm: read response: %w", err)
	}
	if len(raw) > maxResponseBytes {
		return nil, fmt.Errorf("llm: response exceeds %d bytes", maxResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, raw)
	}
	return raw, nil
}

func (r rawResponse) result() (Result, error) {
	if r.Error != nil {
		return Result{}, &APIError{Code: r.Error.Code, Message: r.Error.Message}
	}

	result := Result{ID: r.ID, Model: r.Model}
	if r.Usage != nil {
		result.Usage = *r.Usage
	}

	var text strings.Builder
	for _, item := range r.Output {
		switch item.Type {
		case "message":
			var message strings.Builder
			for _, part := range item.Content {
				if part.Type == "output_text" {
					message.WriteString(part.Text)
					text.WriteString(part.Text)
				}
			}
			if message.Len() > 0 {
				result.Output = append(result.Output, Assistant(message.String()))
			}
		case "function_call":
			callID := item.CallID
			if callID == "" {
				callID = item.ID
			}
			if callID == "" {
				return Result{}, fmt.Errorf("llm: function_call %q missing call_id", item.Name)
			}
			call := ToolCall{
				ID:        item.ID,
				CallID:    callID,
				Name:      item.Name,
				Arguments: item.Arguments,
			}
			result.ToolCalls = append(result.ToolCalls, call)
			result.Output = append(result.Output, FunctionCall{
				ID:        call.ID,
				CallID:    call.CallID,
				Name:      call.Name,
				Arguments: call.Arguments,
			})
		case "reasoning":
			reasoning := Reasoning{
				ID:               item.ID,
				EncryptedContent: item.EncryptedContent,
				Format:           item.Format,
				Signature:        item.Signature,
			}
			for _, part := range item.Summary {
				if part.Type == "summary_text" {
					reasoning.Summary = append(reasoning.Summary, ReasoningSummary{Text: part.Text})
				}
			}
			for _, part := range item.Content {
				if part.Type == "reasoning_text" {
					reasoning.Content = append(reasoning.Content, ReasoningText{Text: part.Text})
				}
			}
			result.Reasoning = append(result.Reasoning, reasoning)
			result.Output = append(result.Output, reasoning)
		}
	}
	result.Text = text.String()

	return result, nil
}

func parseAPIError(status int, raw []byte) error {
	var parsed struct {
		Error *apiError `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err == nil && parsed.Error != nil && parsed.Error.Message != "" {
		return &APIError{Status: status, Code: parsed.Error.Code, Message: parsed.Error.Message}
	}
	return &APIError{Status: status}
}
