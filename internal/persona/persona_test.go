package persona

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxdollinger/meeth-councile/internal/llm"
	"github.com/maxdollinger/meeth-councile/internal/memory"
	"github.com/maxdollinger/meeth-councile/internal/store"
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

type researchCall struct {
	question   string
	background string
	caller     string
}

type fakeResearcher struct {
	calls  []researchCall
	answer string
	err    error
}

func (f *fakeResearcher) Research(_ context.Context, question, background, caller string) (string, llm.Usage, error) {
	f.calls = append(f.calls, researchCall{question: question, background: background, caller: caller})
	return f.answer, llm.Usage{}, f.err
}

func newMemory(t *testing.T, persona string) *memory.Memory {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "persona.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	m, err := memory.New(context.Background(), store.NewMemory(db), "d1", persona)
	if err != nil {
		t.Fatalf("memory.New: %v", err)
	}
	return m
}

func wireJSON(t *testing.T, in llm.Input) string {
	t.Helper()
	wire := make([]map[string]any, 0, len(in))
	for _, item := range in {
		wire = append(wire, item.ResponseItem())
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal wire items: %v", err)
	}
	return string(raw)
}

func TestNewValidation(t *testing.T) {
	mem := newMemory(t, "realism")
	client := &fakeClient{}
	researcher := &fakeResearcher{}

	cases := []struct {
		name       string
		mem        *memory.Memory
		client     CompletionClient
		model      string
		researcher Researcher
	}{
		{"nil memory", nil, client, "m", researcher},
		{"nil client", mem, nil, "m", researcher},
		{"empty model", mem, client, "  ", researcher},
		{"nil researcher", mem, client, "m", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.mem, tc.client, tc.model, tc.researcher); err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestUnderstandIsSingleCompletionBeforeLoop(t *testing.T) {
	fc := &fakeClient{respond: func(_ fakeCall, n int) (llm.Result, error) {
		if n == 1 {
			return llm.Result{Text: "my summary"}, nil
		}
		return llm.Result{Text: "settled understanding"}, nil
	}}
	p, err := New(newMemory(t, "realism"), fc, "m", &fakeResearcher{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	u, err := p.Hear(context.Background(), "error-theory", "there are no moral facts")
	if err != nil {
		t.Fatalf("Hear: %v", err)
	}
	if u.Content != "settled understanding" {
		t.Errorf("Content = %q, want the loop's settled text", u.Content)
	}

	if len(fc.calls) != 2 {
		t.Fatalf("model calls = %d, want 2 (understand + loop)", len(fc.calls))
	}
	last := fc.calls[0].input[len(fc.calls[0].input)-1]
	msg, ok := last.(llm.Message)
	if !ok || msg.Role != llm.RoleUser || !strings.Contains(msg.Content, "record what you now understand") {
		t.Errorf("first call's last item = %#v, want the comprehension instruction", last)
	}
	first := fc.calls[0].input[0]
	if sys, ok := first.(llm.Message); !ok || sys.Role != llm.RoleSystem {
		t.Errorf("first call's first item = %#v, want the system prompt", first)
	}
}

func TestHearStoresHeardTurnThenLoop(t *testing.T) {
	fc := &fakeClient{respond: func(_ fakeCall, n int) (llm.Result, error) {
		if n == 1 {
			return llm.Result{Text: "summary"}, nil
		}
		return llm.Result{Text: "settled understanding"}, nil
	}}
	mem := newMemory(t, "realism")
	p, err := New(mem, fc, "m", &fakeResearcher{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	u, err := p.Hear(context.Background(), "error-theory", "there are no moral facts")
	if err != nil {
		t.Fatalf("Hear: %v", err)
	}
	if u.Speaker != "error-theory" || u.Source != "there are no moral facts" {
		t.Errorf("Understanding = %+v, want speaker and raw source kept", u)
	}
	wantLoop := wireJSON(t, llm.Input{llm.Assistant("settled understanding")})
	if got := wireJSON(t, u.Items); got != wantLoop {
		t.Errorf("Items = %s, want only the new loop %s", got, wantLoop)
	}

	entries, err := mem.Entries(context.Background())
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Kind != memory.KindUnderstanding || e.Speaker != "error-theory" {
		t.Errorf("entry = %+v, want understanding by error-theory", e)
	}
	want := wireJSON(t, llm.Input{
		llm.User("error-theory: settled understanding"),
		llm.Assistant("settled understanding"),
	})
	if got := wireJSON(t, e.Items); got != want {
		t.Errorf("stored items = %s, want heard turn then loop %s", got, want)
	}
}

func TestHearLoopCanUseResearchTool(t *testing.T) {
	fc := &fakeClient{respond: func(_ fakeCall, n int) (llm.Result, error) {
		switch n {
		case 1:
			return llm.Result{Text: "summary"}, nil
		case 2:
			return llm.Result{ToolCalls: []llm.ToolCall{
				{ID: "fc_1", CallID: "call_1", Name: "research_assistant", Arguments: `{"query":"cases of moral disagreement"}`},
			}}, nil
		default:
			return llm.Result{Text: "understanding after research"}, nil
		}
	}}
	rc := &fakeResearcher{answer: "the researched facts"}
	mem := newMemory(t, "realism")
	p, err := New(mem, fc, "m", rc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	u, err := p.Hear(context.Background(), "expressivism", "morality is just attitude")
	if err != nil {
		t.Fatalf("Hear: %v", err)
	}
	if len(rc.calls) != 1 {
		t.Fatalf("research calls = %d, want 1", len(rc.calls))
	}
	if rc.calls[0].question != "cases of moral disagreement" || rc.calls[0].caller != "realism" {
		t.Errorf("research call = %+v, want decoded query and persona as caller", rc.calls[0])
	}
	if u.Content != "understanding after research" {
		t.Errorf("Content = %q, want the post-research text", u.Content)
	}
	if len(u.Items) != 3 {
		t.Fatalf("Items = %d, want function_call, output, assistant", len(u.Items))
	}
	if _, ok := u.Items[0].(llm.FunctionCall); !ok {
		t.Errorf("Items[0] = %T, want FunctionCall", u.Items[0])
	}
	if _, ok := u.Items[1].(llm.FunctionCallOutput); !ok {
		t.Errorf("Items[1] = %T, want FunctionCallOutput", u.Items[1])
	}
}

func TestSpeakStoresAnswerAndReturnsNameContent(t *testing.T) {
	fc := &fakeClient{respond: func(call fakeCall, _ int) (llm.Result, error) {
		return llm.Result{Text: "my argument"}, nil
	}}
	mem := newMemory(t, "realism")
	p, err := New(mem, fc, "m", &fakeResearcher{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	name, content, _, err := p.Speak(context.Background())
	if err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if name != "realism" || content != "my argument" {
		t.Errorf("Speak = (%q, %q), want (realism, my argument)", name, content)
	}

	entries, err := mem.Entries(context.Background())
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Kind != memory.KindAnswer || e.Speaker != "realism" {
		t.Errorf("entry = %+v, want answer by realism", e)
	}
	want := wireJSON(t, llm.Input{llm.Assistant("my argument")})
	if got := wireJSON(t, e.Items); got != want {
		t.Errorf("stored items = %s, want only the new loop %s", got, want)
	}
}

func TestUseModelSwitchesSubsequentCalls(t *testing.T) {
	fc := &fakeClient{respond: func(_ fakeCall, _ int) (llm.Result, error) {
		return llm.Result{Text: "reply"}, nil
	}}
	p, err := New(newMemory(t, "realism"), fc, "m1", &fakeResearcher{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.UseModel("m2"); err != nil {
		t.Fatalf("UseModel: %v", err)
	}
	if _, _, _, err := p.Speak(context.Background()); err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if got := fc.calls[len(fc.calls)-1].model; got != "m2" {
		t.Errorf("model = %q, want m2", got)
	}
	if err := p.UseModel("  "); err == nil {
		t.Error("UseModel blank: want error, got nil")
	}
}

func TestSpeakReadsPriorMemoryWithoutDuplicatingIt(t *testing.T) {
	fc := &fakeClient{respond: func(_ fakeCall, n int) (llm.Result, error) {
		switch n {
		case 1:
			return llm.Result{Text: "summary"}, nil
		case 2:
			return llm.Result{Text: "settled understanding"}, nil
		default:
			return llm.Result{Text: "my reply"}, nil
		}
	}}
	mem := newMemory(t, "realism")
	p, err := New(mem, fc, "m", &fakeResearcher{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.Hear(context.Background(), "error-theory", "no moral facts"); err != nil {
		t.Fatalf("Hear: %v", err)
	}
	if _, _, _, err := p.Speak(context.Background()); err != nil {
		t.Fatalf("Speak: %v", err)
	}

	entries, err := mem.Entries(context.Background())
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want understanding + answer", len(entries))
	}
	answer := entries[1]
	want := wireJSON(t, llm.Input{llm.Assistant("my reply")})
	if got := wireJSON(t, answer.Items); got != want {
		t.Errorf("answer items = %s, want only the new loop %s", got, want)
	}

	speakInput := fc.calls[2].input
	found := false
	for _, item := range speakInput {
		if s, ok := item.ResponseItem()["content"].(string); ok && strings.Contains(s, "error-theory: settled understanding") {
			found = true
		}
	}
	if !found {
		t.Error("Speak input missing the prior understanding from memory")
	}
}
