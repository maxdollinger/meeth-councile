package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/maxdollinger/meeth-councile/internal/llm"
)

type fakeCall struct {
	model string
	input llm.Input
	opts  []llm.ResponseOption
}

type fakeClient struct {
	calls   []fakeCall
	respond func(call fakeCall, n int) (llm.Result, error)
}

func (f *fakeClient) Response(model string, input llm.Input, opts ...llm.ResponseOption) (llm.Result, error) {
	call := fakeCall{model: model, input: input, opts: opts}
	f.calls = append(f.calls, call)
	if f.respond == nil {
		return llm.Result{}, nil
	}
	return f.respond(call, len(f.calls))
}

func TestToolEmbedsDefinition(t *testing.T) {
	tool := NewTool("get_weather", "Get the current weather", map[string]any{"type": "object"}, nil)
	if tool.Name() != "get_weather" {
		t.Errorf("Name() = %q, want get_weather", tool.Name())
	}
	if tool.Description() != "Get the current weather" {
		t.Errorf("Description() = %q, want preserved", tool.Description())
	}
	if tool.Parameters()["type"] != "object" {
		t.Errorf("Parameters() = %v, want object schema", tool.Parameters())
	}
	var _ llm.Tool = tool
}

func TestRunFinalAnswer(t *testing.T) {
	fc := &fakeClient{respond: func(call fakeCall, n int) (llm.Result, error) {
		return llm.Result{
			Text:  "hello",
			Usage: llm.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5, Cost: 0.01},
		}, nil
	}}

	res, err := New(fc, "test-model", "persona").Run(context.Background(), llm.Input{llm.User("hi")})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.Text != "hello" || res.Passed {
		t.Errorf("Result = %+v, want text hello and Passed false", res)
	}
	if res.Steps != 1 {
		t.Errorf("Steps = %d, want 1", res.Steps)
	}
	want := llm.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5, Cost: 0.01}
	if res.Usage != want {
		t.Errorf("Usage = %+v, want %+v", res.Usage, want)
	}

	if fc.calls[0].model != "test-model" {
		t.Errorf("model = %q, want test-model", fc.calls[0].model)
	}
	in := fc.calls[0].input
	if len(in) != 2 {
		t.Fatalf("input = %+v, want system + user", in)
	}
	if msg, ok := in[0].(llm.Message); !ok || msg.Role != llm.RoleSystem || msg.Content != "persona" {
		t.Errorf("input[0] = %+v, want system persona", in[0])
	}
	if msg, ok := in[1].(llm.Message); !ok || msg.Role != llm.RoleUser || msg.Content != "hi" {
		t.Errorf("input[1] = %+v, want user hi", in[1])
	}
}

func TestRunOmitsEmptySystem(t *testing.T) {
	fc := &fakeClient{}
	if _, err := New(fc, "m", "").Run(context.Background(), llm.Input{llm.User("hi")}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	in := fc.calls[0].input
	if len(in) != 1 {
		t.Fatalf("input = %+v, want just the user turn", in)
	}
}

func TestRunDetectsPass(t *testing.T) {
	for _, text := range []string{"PASS", "  PASS\n", "\tPASS\r\n"} {
		fc := &fakeClient{respond: func(fakeCall, int) (llm.Result, error) {
			return llm.Result{Text: text}, nil
		}}
		res, err := New(fc, "m", "s").Run(context.Background(), llm.Input{llm.User("hi")})
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
		if !res.Passed {
			t.Errorf("text %q: Passed = false, want true", text)
		}
	}
}

func TestIsPassRejectsOtherText(t *testing.T) {
	for _, text := range []string{"", "I'll pass", "pass", "PASS."} {
		if IsPass(text) {
			t.Errorf("IsPass(%q) = true, want false", text)
		}
	}
}

func TestRunExecutesToolLoop(t *testing.T) {
	var gotArgs string
	tool := NewTool("get_weather", "Get the weather", map[string]any{"type": "object"}, func(ctx context.Context, args string) (string, error) {
		gotArgs = args
		return `{"temp":72}`, nil
	})

	fc := &fakeClient{respond: func(call fakeCall, n int) (llm.Result, error) {
		if n == 1 {
			return llm.Result{
				Text:      "let me check",
				ToolCalls: []llm.ToolCall{{ID: "fc_1", CallID: "call_1", Name: "get_weather", Arguments: `{"location":"SF"}`}},
			}, nil
		}
		return llm.Result{Text: "72 and sunny"}, nil
	}}

	res, err := New(fc, "m", "persona", WithTools(tool)).Run(context.Background(), llm.Input{llm.User("weather?")})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.Text != "72 and sunny" || res.Steps != 2 {
		t.Errorf("Result = %+v, want final text on step 2", res)
	}
	if gotArgs != `{"location":"SF"}` {
		t.Errorf("Execute args = %q, want raw JSON from the call", gotArgs)
	}

	in := fc.calls[1].input
	want := llm.Input{
		llm.System("persona"),
		llm.User("weather?"),
		llm.Assistant("let me check"),
		llm.FunctionCall{ID: "fc_1", CallID: "call_1", Name: "get_weather", Arguments: `{"location":"SF"}`},
		llm.FunctionCallOutput{CallID: "call_1", Output: `{"temp":72}`},
	}
	if len(in) != len(want) {
		t.Fatalf("second input = %#v, want %#v", in, want)
	}
	for i := range want {
		if in[i] != want[i] {
			t.Errorf("second input[%d] = %#v, want %#v", i, in[i], want[i])
		}
	}

	wantHistory := llm.Input{
		llm.User("weather?"),
		llm.Assistant("let me check"),
		llm.FunctionCall{ID: "fc_1", CallID: "call_1", Name: "get_weather", Arguments: `{"location":"SF"}`},
		llm.FunctionCallOutput{CallID: "call_1", Output: `{"temp":72}`},
		llm.Assistant("72 and sunny"),
	}
	if !reflect.DeepEqual(res.History, wantHistory) {
		t.Errorf("History = %#v, want %#v", res.History, wantHistory)
	}
}

func TestRunHistoryPreservesReasoning(t *testing.T) {
	tool := NewTool("noop", "d", map[string]any{"type": "object"}, func(context.Context, string) (string, error) {
		return "ok", nil
	})
	reasoning := llm.Reasoning{
		ID:               "rs_1",
		Summary:          []llm.ReasoningSummary{{Text: "weigh options"}},
		Content:          []llm.ReasoningText{{Text: "step by step"}},
		EncryptedContent: "enc_abc",
	}
	call := llm.FunctionCall{ID: "fc_1", CallID: "call_1", Name: "noop", Arguments: `{}`}
	fc := &fakeClient{respond: func(_ fakeCall, n int) (llm.Result, error) {
		if n == 1 {
			return llm.Result{
				Output:    llm.Input{reasoning, call},
				ToolCalls: []llm.ToolCall{{ID: "fc_1", CallID: "call_1", Name: "noop", Arguments: `{}`}},
			}, nil
		}
		return llm.Result{Text: "done"}, nil
	}}

	res, err := New(fc, "m", "s", WithTools(tool)).Run(context.Background(), llm.Input{llm.User("go")})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	want := llm.Input{
		llm.User("go"),
		reasoning,
		call,
		llm.FunctionCallOutput{CallID: "call_1", Output: "ok"},
		llm.Assistant("done"),
	}
	if !reflect.DeepEqual(res.History, want) {
		t.Errorf("History = %#v, want %#v", res.History, want)
	}
}

func TestRunHistoryResumesConversation(t *testing.T) {
	tool := NewTool("noop", "d", map[string]any{"type": "object"}, func(context.Context, string) (string, error) {
		return "ok", nil
	})
	fc := &fakeClient{respond: func(_ fakeCall, n int) (llm.Result, error) {
		if n == 1 {
			return llm.Result{
				Output: llm.Input{
					llm.Reasoning{ID: "rs_1", EncryptedContent: "enc"},
					llm.FunctionCall{CallID: "call_1", Name: "noop", Arguments: `{}`},
				},
				ToolCalls: []llm.ToolCall{{CallID: "call_1", Name: "noop", Arguments: `{}`}},
			}, nil
		}
		return llm.Result{Text: "done"}, nil
	}}
	a := New(fc, "m", "persona", WithTools(tool))

	first, err := a.Run(context.Background(), llm.Input{llm.User("go")})
	if err != nil {
		t.Fatalf("first Run returned error: %v", err)
	}
	if _, err := a.Run(context.Background(), first.History); err != nil {
		t.Fatalf("resumed Run returned error: %v", err)
	}

	want := append(llm.Input{llm.System("persona")}, first.History...)
	if !reflect.DeepEqual(fc.calls[2].input, want) {
		t.Errorf("resumed input = %#v, want %#v", fc.calls[2].input, want)
	}
}

func TestRunHistoryOnMaxSteps(t *testing.T) {
	tool := NewTool("loop", "d", map[string]any{"type": "object"}, func(context.Context, string) (string, error) {
		return "again", nil
	})
	fc := &fakeClient{respond: func(fakeCall, int) (llm.Result, error) {
		return llm.Result{ToolCalls: []llm.ToolCall{{CallID: "c", Name: "loop", Arguments: `{}`}}}, nil
	}}

	res, err := New(fc, "m", "s", WithTools(tool), WithMaxSteps(1)).Run(context.Background(), llm.Input{llm.User("go")})
	if err == nil {
		t.Fatal("expected max-steps error, got nil")
	}
	want := llm.Input{
		llm.User("go"),
		llm.FunctionCall{CallID: "c", Name: "loop", Arguments: `{}`},
		llm.FunctionCallOutput{CallID: "c", Output: "again"},
	}
	if !reflect.DeepEqual(res.History, want) {
		t.Errorf("History = %#v, want %#v", res.History, want)
	}
}

func TestRunFallsBackToCallIDFromID(t *testing.T) {
	tool := NewTool("noop", "d", map[string]any{"type": "object"}, func(context.Context, string) (string, error) {
		return "ok", nil
	})
	fc := &fakeClient{respond: func(call fakeCall, n int) (llm.Result, error) {
		if n == 1 {
			return llm.Result{ToolCalls: []llm.ToolCall{{ID: "fc_9", Name: "noop", Arguments: `{}`}}}, nil
		}
		return llm.Result{Text: "done"}, nil
	}}

	if _, err := New(fc, "m", "s", WithTools(tool)).Run(context.Background(), llm.Input{llm.User("go")}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	in := fc.calls[1].input
	out, ok := in[len(in)-1].(llm.FunctionCallOutput)
	if !ok {
		t.Fatalf("last input = %T, want FunctionCallOutput", in[len(in)-1])
	}
	if out.CallID != "fc_9" {
		t.Errorf("CallID = %q, want fallback to ID fc_9", out.CallID)
	}
}

func TestRunFeedsBackUnknownTool(t *testing.T) {
	fc := &fakeClient{respond: func(call fakeCall, n int) (llm.Result, error) {
		if n == 1 {
			return llm.Result{ToolCalls: []llm.ToolCall{{ID: "fc_1", CallID: "call_1", Name: "missing", Arguments: `{}`}}}, nil
		}
		return llm.Result{Text: "recovered"}, nil
	}}

	if _, err := New(fc, "m", "s").Run(context.Background(), llm.Input{llm.User("go")}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	in := fc.calls[1].input
	out, ok := in[len(in)-1].(llm.FunctionCallOutput)
	if !ok {
		t.Fatalf("second input = %+v, want a FunctionCallOutput", in)
	}
	if !strings.Contains(out.Output, "unknown tool: missing") {
		t.Errorf("output = %q, want unknown-tool error", out.Output)
	}
	if !strings.HasPrefix(out.Output, "{") {
		t.Errorf("output = %q, want a JSON object", out.Output)
	}
}

func TestRunFeedsBackToolError(t *testing.T) {
	tool := NewTool("boom", "d", map[string]any{"type": "object"}, func(context.Context, string) (string, error) {
		return "", errors.New("kaboom")
	})
	fc := &fakeClient{respond: func(call fakeCall, n int) (llm.Result, error) {
		if n == 1 {
			return llm.Result{ToolCalls: []llm.ToolCall{{CallID: "call_1", Name: "boom", Arguments: `{}`}}}, nil
		}
		return llm.Result{Text: "handled"}, nil
	}}

	if _, err := New(fc, "m", "s", WithTools(tool)).Run(context.Background(), llm.Input{llm.User("go")}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	in := fc.calls[1].input
	out, ok := in[len(in)-1].(llm.FunctionCallOutput)
	if !ok {
		t.Fatalf("second input = %+v, want a FunctionCallOutput", in)
	}
	if !strings.Contains(out.Output, "kaboom") {
		t.Errorf("output = %q, want the handler error", out.Output)
	}
}

func TestRunAccumulatesUsage(t *testing.T) {
	tool := NewTool("noop", "d", map[string]any{"type": "object"}, func(context.Context, string) (string, error) {
		return "ok", nil
	})
	fc := &fakeClient{respond: func(call fakeCall, n int) (llm.Result, error) {
		if n == 1 {
			return llm.Result{
				ToolCalls: []llm.ToolCall{{CallID: "call_1", Name: "noop", Arguments: `{}`}},
				Usage:     llm.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3, Cost: 0.1},
			}, nil
		}
		return llm.Result{
			Text:  "done",
			Usage: llm.Usage{InputTokens: 4, OutputTokens: 5, TotalTokens: 9, Cost: 0.2},
		}, nil
	}}

	res, err := New(fc, "m", "s", WithTools(tool)).Run(context.Background(), llm.Input{llm.User("go")})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	want := llm.Usage{InputTokens: 5, OutputTokens: 7, TotalTokens: 12, Cost: 0.30000000000000004}
	if res.Usage != want {
		t.Errorf("Usage = %+v, want %+v", res.Usage, want)
	}
}

func TestRunMaxSteps(t *testing.T) {
	tool := NewTool("loop", "d", map[string]any{"type": "object"}, func(context.Context, string) (string, error) {
		return "again", nil
	})
	fc := &fakeClient{respond: func(call fakeCall, n int) (llm.Result, error) {
		return llm.Result{ToolCalls: []llm.ToolCall{{CallID: "c", Name: "loop", Arguments: `{}`}}}, nil
	}}

	_, err := New(fc, "m", "s", WithTools(tool), WithMaxSteps(2)).Run(context.Background(), llm.Input{llm.User("go")})
	if err == nil || !strings.Contains(err.Error(), "exceeded max steps (2)") {
		t.Fatalf("error = %v, want max-steps error", err)
	}
	if len(fc.calls) != 2 {
		t.Errorf("model calls = %d, want 2", len(fc.calls))
	}
}

func TestCallOptions(t *testing.T) {
	fc := &fakeClient{}
	tool := NewTool("t", "d", map[string]any{"type": "object"}, nil)

	if got := New(fc, "m", "s").callOptions(context.Background()); len(got) != 1 {
		t.Errorf("options with no tools = %d, want 1 (just context)", len(got))
	}
	if got := New(fc, "m", "s", WithTools(tool)).callOptions(context.Background()); len(got) != 2 {
		t.Errorf("options with a tool = %d, want 2 (tools + context)", len(got))
	}
}
