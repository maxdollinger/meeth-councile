package llm

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c := New("test-key", srv.Client())
	c.baseURL = srv.URL
	return c
}

func TestResponseSuccess(t *testing.T) {
	const body = `{
		"id": "resp-abc123",
		"model": "gpt-4",
		"object": "response",
		"status": "completed",
		"output": [
			{
				"type": "message",
				"id": "msg-abc123",
				"role": "assistant",
				"status": "completed",
				"content": [
					{"type": "output_text", "text": "Hello! How can I help you today?", "annotations": []}
				]
			}
		],
		"usage": {
			"input_tokens": 10,
			"output_tokens": 25,
			"total_tokens": 35,
			"cost": 0.0012
		}
	}`

	var gotPath, gotMethod, gotAuth, gotContentType string
	var gotBody request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	defer srv.Close()

	result, err := newTestClient(t, srv).Response("gpt-4", Input{User("hi there")})
	if err != nil {
		t.Fatalf("Response returned error: %v", err)
	}

	if result.Text != "Hello! How can I help you today?" {
		t.Errorf("Text = %q, want %q", result.Text, "Hello! How can I help you today?")
	}
	if result.ID != "resp-abc123" {
		t.Errorf("ID = %q, want %q", result.ID, "resp-abc123")
	}
	if result.Model != "gpt-4" {
		t.Errorf("Model = %q, want %q", result.Model, "gpt-4")
	}
	wantUsage := Usage{InputTokens: 10, OutputTokens: 25, TotalTokens: 35, Cost: 0.0012}
	if result.Usage != wantUsage {
		t.Errorf("Usage = %+v, want %+v", result.Usage, wantUsage)
	}

	if gotPath != "/responses" {
		t.Errorf("path = %q, want /responses", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-key")
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotBody.Model != "gpt-4" {
		t.Errorf("Model = %q, want %q", gotBody.Model, "gpt-4")
	}
	if len(gotBody.Input) != 1 {
		t.Fatalf("Input = %+v, want one item", gotBody.Input)
	}
	msg := gotBody.Input[0]
	if msg["type"] != "message" || msg["role"] != "user" || msg["content"] != "hi there" {
		t.Errorf("Input[0] = %+v, want message/user/hi there", msg)
	}
}

func TestResponseAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"code":400,"message":"Invalid request parameters"}}`)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Response("gpt-4", Input{User("hi")})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "Invalid request parameters") {
		t.Errorf("error = %q, want it to mention status and message", err)
	}
}

func TestResponseErrorField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":"resp-1","model":"gpt-4","error":{"code":500,"message":"upstream exploded"}}`)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Response("gpt-4", Input{User("hi")})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "upstream exploded") {
		t.Errorf("error = %q, want it to contain the API error message", err)
	}
}

func TestResponseIgnoresNonOutputText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{
			"id":"resp-1",
			"model":"gpt-4",
			"output":[
				{"type":"reasoning","content":[{"type":"reasoning_text","text":"thinking..."}]},
				{"type":"message","content":[
					{"type":"refusal","text":"I cannot do that"},
					{"type":"output_text","text":"the answer"}
				]}
			]
		}`)
	}))
	defer srv.Close()

	result, err := newTestClient(t, srv).Response("gpt-4", Input{User("hi")})
	if err != nil {
		t.Fatalf("Response returned error: %v", err)
	}
	if result.Text != "the answer" {
		t.Errorf("Text = %q, want %q", result.Text, "the answer")
	}
	if result.Usage != (Usage{}) {
		t.Errorf("Usage = %+v, want zero value", result.Usage)
	}
}

func TestResponseNonJSONErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "<html>bad gateway</html>")
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Response("gpt-4", Input{User("hi")})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error = %q, want it to mention status 502", err)
	}
}

func TestResponseTypedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"code":429,"message":"Rate limit exceeded"}}`)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Response("gpt-4", Input{User("hi")})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v is not *APIError", err)
	}
	if apiErr.Status != http.StatusTooManyRequests {
		t.Errorf("Status = %d, want %d", apiErr.Status, http.StatusTooManyRequests)
	}
	if apiErr.Code != 429 || apiErr.Message != "Rate limit exceeded" {
		t.Errorf("APIError = %+v, want {Code:429 Message:%q}", apiErr, "Rate limit exceeded")
	}
}

func TestNewPanicsOnNilHTTPClient(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New with nil http client did not panic")
		}
	}()
	New("test-key", nil)
}

func TestNewPanicsOnEmptyAPIKey(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New with empty api key did not panic")
		}
	}()
	New("", http.DefaultClient)
}

func TestResponseOptions(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		io.WriteString(w, `{"id":"resp-1","model":"gpt-4","output":[]}`)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Response("gpt-4", Input{User("hi")}, WithMaxOutputTokens(256), WithTemperature(0.7))
	if err != nil {
		t.Fatalf("Response returned error: %v", err)
	}
	if got := gotBody["max_output_tokens"]; got != float64(256) {
		t.Errorf("max_output_tokens = %v, want 256", got)
	}
	if got := gotBody["temperature"]; got != 0.7 {
		t.Errorf("temperature = %v, want 0.7", got)
	}
}

func TestResponseOmitsUnsetOptions(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		io.WriteString(w, `{"id":"resp-1","model":"gpt-4","output":[]}`)
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv).Response("gpt-4", Input{User("hi")}); err != nil {
		t.Fatalf("Response returned error: %v", err)
	}
	if _, ok := gotBody["max_output_tokens"]; ok {
		t.Error("max_output_tokens present, want omitted")
	}
	if _, ok := gotBody["temperature"]; ok {
		t.Error("temperature present, want omitted")
	}
	if _, ok := gotBody["tools"]; ok {
		t.Error("tools present, want omitted")
	}
	if _, ok := gotBody["tool_choice"]; ok {
		t.Error("tool_choice present, want omitted")
	}
}

func TestResponseSendsTools(t *testing.T) {
	var gotBody request
	var gotRaw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_ = json.Unmarshal(raw, &gotRaw)
		io.WriteString(w, `{"id":"resp-1","model":"gpt-4","output":[]}`)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Response("gpt-4", Input{User("hi")}, WithTools(
		NewFunctionTool("get_weather", "Get the current weather in a location", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"location": map[string]any{"type": "string"},
			},
			"required": []any{"location"},
		}),
	))
	if err != nil {
		t.Fatalf("Response returned error: %v", err)
	}

	if gotBody.ToolChoice != "auto" {
		t.Errorf("tool_choice = %q, want %q", gotBody.ToolChoice, "auto")
	}
	if len(gotBody.Tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(gotBody.Tools))
	}
	tool := gotBody.Tools[0]
	if tool.Type != "function" {
		t.Errorf("tool.type = %q, want %q", tool.Type, "function")
	}
	if tool.Name != "get_weather" {
		t.Errorf("tool.name = %q, want %q", tool.Name, "get_weather")
	}
	if tool.Description != "Get the current weather in a location" {
		t.Errorf("tool.description = %q, want it preserved", tool.Description)
	}
	if tool.Parameters["type"] != "object" {
		t.Errorf("tool.parameters.type = %v, want object", tool.Parameters["type"])
	}
	props, ok := tool.Parameters["properties"].(map[string]any)
	if !ok || props["location"] == nil {
		t.Errorf("tool.parameters.properties = %v, want location property", tool.Parameters["properties"])
	}

	// Confirm the wire shape is flat (name/description/parameters as siblings),
	// not nested under a "function" key.
	rawTools, ok := gotRaw["tools"].([]any)
	if !ok || len(rawTools) != 1 {
		t.Fatalf("raw tools = %v, want one entry", gotRaw["tools"])
	}
	rawTool, _ := rawTools[0].(map[string]any)
	for _, key := range []string{"type", "name", "description", "parameters"} {
		if _, ok := rawTool[key]; !ok {
			t.Errorf("tool missing top-level %q key; got %v", key, rawTool)
		}
	}
	if _, nested := rawTool["function"]; nested {
		t.Errorf("tool unexpectedly nested under 'function': %v", rawTool)
	}
}

func TestResponseParsesFunctionCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{
			"id":"resp-1",
			"model":"gpt-4",
			"output":[
				{"type":"message","content":[{"type":"output_text","text":"let me check"}]},
				{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":"{\"location\":\"San Francisco, CA\"}"},
				{"type":"function_call","id":"fc_2","call_id":"call_2","name":"calculate","arguments":"{\"expression\":\"25 * 4\"}"}
			]
		}`)
	}))
	defer srv.Close()

	result, err := newTestClient(t, srv).Response("gpt-4", Input{User("hi")})
	if err != nil {
		t.Fatalf("Response returned error: %v", err)
	}
	if result.Text != "let me check" {
		t.Errorf("Text = %q, want %q", result.Text, "let me check")
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("len(ToolCalls) = %d, want 2", len(result.ToolCalls))
	}

	first := result.ToolCalls[0]
	if first.ID != "fc_1" || first.CallID != "call_1" || first.Name != "get_weather" {
		t.Errorf("first call = %+v, want {ID:fc_1 CallID:call_1 Name:get_weather}", first)
	}
	var weather struct {
		Location string `json:"location"`
	}
	if err := first.Args(&weather); err != nil {
		t.Fatalf("Args returned error: %v", err)
	}
	if weather.Location != "San Francisco, CA" {
		t.Errorf("location = %q, want %q", weather.Location, "San Francisco, CA")
	}

	if second := result.ToolCalls[1]; second.Name != "calculate" || second.CallID != "call_2" {
		t.Errorf("second call = %+v, want calculate/call_2", second)
	}
}

func TestInputWire(t *testing.T) {
	in := Input{
		System("persona"),
		User("hi"),
		Assistant("hello"),
		FunctionCall{ID: "fc_1", CallID: "call_1", Name: "get_weather", Arguments: `{"location":"SF"}`},
		FunctionCall{CallID: "call_2", Name: "calculate", Arguments: `{}`},
		FunctionCallOutput{CallID: "call_1", Output: `{"temp":72}`},
		Reasoning{
			ID:               "rs_1",
			Summary:          []ReasoningSummary{{Text: "thought about it"}},
			Content:          []ReasoningText{{Text: "step by step"}},
			EncryptedContent: "enc_abc",
		},
		Reasoning{ID: "rs_2"},
		nil,
	}

	got := in.wire()
	if len(got) != 8 {
		t.Fatalf("len(wire) = %d, want 8 (nil skipped)", len(got))
	}

	if want := map[string]any{"type": "message", "role": "system", "content": "persona"}; !reflect.DeepEqual(got[0], want) {
		t.Errorf("wire[0] = %+v, want %+v", got[0], want)
	}
	if want := map[string]any{"type": "message", "role": "user", "content": "hi"}; !reflect.DeepEqual(got[1], want) {
		t.Errorf("wire[1] = %+v, want %+v", got[1], want)
	}
	if want := map[string]any{"type": "message", "role": "assistant", "content": "hello"}; !reflect.DeepEqual(got[2], want) {
		t.Errorf("wire[2] = %+v, want %+v", got[2], want)
	}
	if want := map[string]any{
		"type":      "function_call",
		"id":        "fc_1",
		"call_id":   "call_1",
		"name":      "get_weather",
		"arguments": `{"location":"SF"}`,
	}; !reflect.DeepEqual(got[3], want) {
		t.Errorf("wire[3] = %+v, want %+v", got[3], want)
	}
	if _, ok := got[4]["id"]; ok {
		t.Errorf("wire[4] = %+v, want id omitted when empty", got[4])
	}
	if want := map[string]any{"type": "function_call_output", "call_id": "call_1", "output": `{"temp":72}`}; !reflect.DeepEqual(got[5], want) {
		t.Errorf("wire[5] = %+v, want %+v", got[5], want)
	}
	if want := map[string]any{
		"type":              "reasoning",
		"id":                "rs_1",
		"summary":           []map[string]any{{"type": "summary_text", "text": "thought about it"}},
		"content":           []map[string]any{{"type": "reasoning_text", "text": "step by step"}},
		"encrypted_content": "enc_abc",
	}; !reflect.DeepEqual(got[6], want) {
		t.Errorf("wire[6] = %+v, want %+v", got[6], want)
	}
	if want := map[string]any{
		"type":    "reasoning",
		"id":      "rs_2",
		"summary": []map[string]any{},
	}; !reflect.DeepEqual(got[7], want) {
		t.Errorf("wire[7] = %+v, want %+v", got[7], want)
	}
}

func TestResultItems(t *testing.T) {
	result := Result{
		Text: "let me check",
		ToolCalls: []ToolCall{
			{ID: "fc_1", CallID: "call_1", Name: "get_weather", Arguments: `{"location":"SF"}`},
			{ID: "fc_2", CallID: "call_2", Name: "calculate", Arguments: `{"expression":"25*4"}`},
		},
	}

	got := result.Items()
	want := Input{
		Assistant("let me check"),
		FunctionCall{ID: "fc_1", CallID: "call_1", Name: "get_weather", Arguments: `{"location":"SF"}`},
		FunctionCall{ID: "fc_2", CallID: "call_2", Name: "calculate", Arguments: `{"expression":"25*4"}`},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Items() = %#v, want %#v", got, want)
	}
}

func TestResultItemsOmitsEmptyText(t *testing.T) {
	result := Result{ToolCalls: []ToolCall{{ID: "fc_1", CallID: "call_1", Name: "noop"}}}
	got := result.Items()
	if _, ok := got[0].(Message); ok {
		t.Errorf("Items() = %#v, want no leading assistant message", got)
	}
}

func TestResultItemsFallsBackToID(t *testing.T) {
	result := Result{ToolCalls: []ToolCall{{ID: "fc_1", Name: "get_weather", Arguments: `{}`}}}
	got := result.Items()
	call, ok := got[0].(FunctionCall)
	if !ok {
		t.Fatalf("Items()[0] = %T, want FunctionCall", got[0])
	}
	if call.CallID != "fc_1" {
		t.Errorf("CallID = %q, want fallback to ID %q", call.CallID, "fc_1")
	}
}

func TestResponseRequiresInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server called with empty input")
	}))
	defer srv.Close()

	for _, in := range []Input{nil, {}, {nil}} {
		_, err := newTestClient(t, srv).Response("gpt-4", in)
		if err == nil {
			t.Fatalf("Response(%#v) expected error, got nil", in)
		}
		if !strings.Contains(err.Error(), "input is required") {
			t.Errorf("Response(%#v) error = %q, want it to mention required input", in, err)
		}
	}
}

func TestResponseParsesReasoning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{
			"id":"resp-1",
			"model":"gpt-4",
			"output":[
				{
					"type":"reasoning",
					"id":"rs_1",
					"summary":[{"type":"summary_text","text":"weigh options"}],
					"content":[{"type":"reasoning_text","text":"first, consider"}],
					"encrypted_content":"enc_abc",
					"format":"openai-responses-v1",
					"signature":"sig"
				},
				{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":"{\"location\":\"SF\"}"},
				{"type":"message","content":[{"type":"output_text","text":"done"}]}
			]
		}`)
	}))
	defer srv.Close()

	result, err := newTestClient(t, srv).Response("gpt-4", Input{User("hi")})
	if err != nil {
		t.Fatalf("Response returned error: %v", err)
	}

	if len(result.Reasoning) != 1 {
		t.Fatalf("len(Reasoning) = %d, want 1", len(result.Reasoning))
	}
	reasoning := result.Reasoning[0]
	if reasoning.ID != "rs_1" || reasoning.EncryptedContent != "enc_abc" || reasoning.Format != "openai-responses-v1" || reasoning.Signature != "sig" {
		t.Errorf("Reasoning = %+v, want id/encrypted/format/signature populated", reasoning)
	}
	if len(reasoning.Summary) != 1 || reasoning.Summary[0].Text != "weigh options" {
		t.Errorf("Reasoning.Summary = %+v, want one summary_text", reasoning.Summary)
	}
	if len(reasoning.Content) != 1 || reasoning.Content[0].Text != "first, consider" {
		t.Errorf("Reasoning.Content = %+v, want one reasoning_text", reasoning.Content)
	}

	if result.Text != "done" {
		t.Errorf("Text = %q, want %q", result.Text, "done")
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].CallID != "call_1" {
		t.Errorf("ToolCalls = %+v, want one call_1", result.ToolCalls)
	}

	want := Input{
		Reasoning{
			ID:               "rs_1",
			Summary:          []ReasoningSummary{{Text: "weigh options"}},
			Content:          []ReasoningText{{Text: "first, consider"}},
			EncryptedContent: "enc_abc",
			Format:           "openai-responses-v1",
			Signature:        "sig",
		},
		FunctionCall{ID: "fc_1", CallID: "call_1", Name: "get_weather", Arguments: `{"location":"SF"}`},
		Assistant("done"),
	}
	if !reflect.DeepEqual(Input(result.Output), want) {
		t.Errorf("Output = %#v, want ordered %#v", result.Output, want)
	}
}

func TestResultItemsIncludesReasoning(t *testing.T) {
	output := Input{
		Reasoning{ID: "rs_1", Summary: []ReasoningSummary{{Text: "hmm"}}},
		FunctionCall{ID: "fc_1", CallID: "call_1", Name: "get_weather", Arguments: `{}`},
	}
	result := Result{Text: "checking", Output: output}

	got := result.Items()
	if !reflect.DeepEqual(got, output) {
		t.Errorf("Items() = %#v, want Output %#v in order", got, output)
	}
}

func TestResponseSendsInclude(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		io.WriteString(w, `{"id":"resp-1","model":"gpt-4","output":[]}`)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Response("gpt-4", Input{User("hi")}, WithInclude("reasoning.encrypted_content"))
	if err != nil {
		t.Fatalf("Response returned error: %v", err)
	}
	include, ok := gotBody["include"].([]any)
	if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Errorf("include = %v, want [reasoning.encrypted_content]", gotBody["include"])
	}
}

func TestResponseRejectsCallWithoutCallID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{
			"id":"resp-1",
			"model":"gpt-4",
			"output":[{"type":"function_call","name":"get_weather","arguments":"{}"}]
		}`)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Response("gpt-4", Input{User("hi")})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "missing call_id") {
		t.Errorf("error = %q, want it to mention missing call_id", err)
	}
}
