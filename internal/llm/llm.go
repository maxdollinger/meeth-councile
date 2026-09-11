package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/maxdollinger/meeth-councile/internal/logging"
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
	logger  *slog.Logger

	mu        sync.Mutex
	totalCost float64
}

func New(apiKey string, httpClient *http.Client, opts ...Option) *Client {
	if apiKey == "" {
		panic("llm: api key is required")
	}
	if httpClient == nil {
		panic("llm: http client is required")
	}
	c := &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		http:    httpClient,
		logger:  logging.Discard(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) Response(model string, input Input, opts ...ResponseOption) (Result, error) {
	start := time.Now()
	wired := input.wire()
	if len(wired) == 0 {
		return Result{}, errors.New("llm: input is required")
	}
	req := request{Model: model, Input: wired}
	for _, opt := range opts {
		opt(&req)
	}

	raw, err := c.do(req.ctx, req)
	if err != nil {
		c.logger.Warn("llm request failed", "model", model, "duration", time.Since(start), "err", err)
		return Result{}, err
	}

	var parsed rawResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		c.logger.Warn("llm response decode failed", "model", model, "duration", time.Since(start), "err", err)
		return Result{}, fmt.Errorf("llm: decode response: %w", err)
	}
	res, err := parsed.result()
	if err != nil {
		c.logger.Warn("llm response error", "model", model, "duration", time.Since(start), "err", err)
		return Result{}, err
	}
	c.logger.Info("llm call",
		"model", res.Model,
		"id", res.ID,
		"duration", time.Since(start),
		"input_tokens", res.Usage.InputTokens,
		"output_tokens", res.Usage.OutputTokens,
		"total_tokens", res.Usage.TotalTokens,
		"cost", res.Usage.Cost,
		"total_cost", c.addCost(res.Usage.Cost),
		"tool_calls", len(res.ToolCalls),
	)
	return res, nil
}

// addCost adds a call's cost to the client's running total and returns it.
func (c *Client) addCost(cost float64) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.totalCost += cost
	return c.totalCost
}

// do sends body to the /responses endpoint and returns the raw response body.
// It owns request construction, headers, transport, and HTTP-status errors so
// that every OpenRouter call site shares one implementation.
func (c *Client) do(ctx context.Context, body any) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("llm: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(payload))
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
				if part.Type == "output_text" || part.Type == "" {
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
				if part.Type == "summary_text" || part.Type == "" {
					reasoning.Summary = append(reasoning.Summary, ReasoningSummary{Text: part.Text})
				}
			}
			for _, part := range item.Content {
				if part.Type == "reasoning_text" || part.Type == "" {
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
