package persona

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
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
	question string
	caller   string
}

type fakeResearcher struct {
	calls  []researchCall
	answer string
	err    error
}

func (f *fakeResearcher) Research(_ context.Context, question, caller string) (string, llm.Usage, error) {
	f.calls = append(f.calls, researchCall{question: question, caller: caller})
	return f.answer, llm.Usage{}, f.err
}

func newMemory(t *testing.T, persona string) *memory.Memory {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "persona.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	m, err := memory.New(context.Background(), store.NewMemory(db), persona)
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

func TestHearIsSingleComprehension(t *testing.T) {
	fc := &fakeClient{respond: func(_ fakeCall, _ int) (llm.Result, error) {
		return llm.Result{Text: "my summary"}, nil
	}}
	p, err := New(newMemory(t, "realism"), fc, "m", &fakeResearcher{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	u, _, err := p.Hear(context.Background(), "error-theory", "there are no moral facts")
	if err != nil {
		t.Fatalf("Hear: %v", err)
	}
	if u.Content != "my summary" {
		t.Errorf("Content = %q, want the comprehension summary", u.Content)
	}

	if len(fc.calls) != 1 {
		t.Fatalf("model calls = %d, want 1 (a single comprehension)", len(fc.calls))
	}
	last := fc.calls[0].input[len(fc.calls[0].input)-1]
	msg, ok := last.(llm.Message)
	if !ok || msg.Role != llm.RoleUser || !strings.Contains(msg.Content, "Halte in deinen eigenen Worten fest") {
		t.Errorf("call's last item = %#v, want the comprehension instruction", last)
	}
	first := fc.calls[0].input[0]
	if sys, ok := first.(llm.Message); !ok || sys.Role != llm.RoleSystem {
		t.Errorf("call's first item = %#v, want the system prompt", first)
	}
}

func TestHearStoresUnderstandingOnly(t *testing.T) {
	fc := &fakeClient{respond: func(_ fakeCall, _ int) (llm.Result, error) {
		return llm.Result{Text: "summary"}, nil
	}}
	mem := newMemory(t, "realism")
	p, err := New(mem, fc, "m", &fakeResearcher{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	u, _, err := p.Hear(context.Background(), "error-theory", "there are no moral facts")
	if err != nil {
		t.Fatalf("Hear: %v", err)
	}
	if u.Speaker != "error-theory" || u.Source != "there are no moral facts" {
		t.Errorf("Understanding = %+v, want speaker and raw source kept", u)
	}
	if len(u.Items) != 0 {
		t.Errorf("Items = %v, want none without a hear loop", u.Items)
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
		llm.User("error-theory: summary"),
	})
	if got := wireJSON(t, e.Items); got != want {
		t.Errorf("stored items = %s, want the labeled understanding %s", got, want)
	}
}

func TestHearDirectStoresQuestionWithoutModelCall(t *testing.T) {
	ctx := context.Background()
	fc := &fakeClient{}
	mem := newMemory(t, "realism")
	p, err := New(mem, fc, "m", &fakeResearcher{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	question := "Are there mind-independent moral facts?"
	u, err := p.HearDirect(ctx, "moderator", question)
	if err != nil {
		t.Fatalf("HearDirect: %v", err)
	}
	if u.Content != question || u.Source != question {
		t.Errorf("Understanding = %+v, want the question verbatim", u)
	}
	if len(fc.calls) != 0 {
		t.Errorf("model calls = %d, want 0 for a direct hear", len(fc.calls))
	}

	entries, err := mem.Entries(ctx)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Kind != memory.KindUnderstanding || entries[0].Content != question {
		t.Errorf("entry = %+v, want the stored question", entries[0])
	}
}

func TestHearDoesNotUseResearchTool(t *testing.T) {
	fc := &fakeClient{respond: func(_ fakeCall, _ int) (llm.Result, error) {
		return llm.Result{Text: "summary"}, nil
	}}
	rc := &fakeResearcher{answer: "the researched facts"}
	mem := newMemory(t, "realism")
	p, err := New(mem, fc, "m", rc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, _, err := p.Hear(context.Background(), "expressivism", "morality is just attitude"); err != nil {
		t.Fatalf("Hear: %v", err)
	}
	if len(rc.calls) != 0 {
		t.Fatalf("research calls = %d, want 0 in Hear", len(rc.calls))
	}
	if len(fc.calls) != 1 {
		t.Fatalf("model calls = %d, want 1", len(fc.calls))
	}
}

func TestSpeakStoresAnswerAndReturnsNameContent(t *testing.T) {
	fc := &fakeClient{respond: func(call fakeCall, _ int) (llm.Result, error) {
		return llm.Result{
			Text:  "my argument",
			Usage: llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15, Cost: 0.01},
		}, nil
	}}
	mem := newMemory(t, "realism")
	p, err := New(mem, fc, "m", &fakeResearcher{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	name, content, usage, _, err := p.Speak(context.Background())
	if err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if name != "realism" || content != "my argument" {
		t.Errorf("Speak = (%q, %q), want (realism, my argument)", name, content)
	}
	if usage.TotalTokens != 15 || usage.Cost != 0.01 {
		t.Errorf("usage = %+v, want the loop's usage and cost", usage)
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
	if _, _, _, _, err := p.Speak(context.Background()); err != nil {
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
		default:
			return llm.Result{Text: "my reply"}, nil
		}
	}}
	mem := newMemory(t, "realism")
	p, err := New(mem, fc, "m", &fakeResearcher{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, _, err := p.Hear(context.Background(), "error-theory", "no moral facts"); err != nil {
		t.Fatalf("Hear: %v", err)
	}
	if _, _, _, _, err := p.Speak(context.Background()); err != nil {
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

	speakInput := fc.calls[1].input
	found := false
	for _, item := range speakInput {
		if s, ok := item.ResponseItem()["content"].(string); ok && strings.Contains(s, "error-theory: summary") {
			found = true
		}
	}
	if !found {
		t.Error("Speak input missing the prior understanding from memory")
	}
}

func TestLogsPersonaAndCurrentModelOnce(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	fc := &fakeClient{respond: func(_ fakeCall, _ int) (llm.Result, error) {
		return llm.Result{Text: "summary"}, nil
	}}
	p, err := New(newMemory(t, "realism"), fc, "m1", &fakeResearcher{}, WithLogger(logger))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, _, err := p.Hear(context.Background(), "error-theory", "no moral facts"); err != nil {
		t.Fatalf("Hear: %v", err)
	}
	if err := p.UseModel("m2"); err != nil {
		t.Fatalf("UseModel: %v", err)
	}
	if _, _, _, _, err := p.Speak(context.Background()); err != nil {
		t.Fatalf("Speak: %v", err)
	}

	lines := 0
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines++
		if got := strings.Count(line, "persona="); got != 1 {
			t.Errorf("persona logged %d times, want 1: %s", got, line)
		}
		if got := strings.Count(line, "model="); got != 1 {
			t.Errorf("model logged %d times, want 1: %s", got, line)
		}
	}
	if lines == 0 {
		t.Fatal("no log records written")
	}
	out := buf.String()
	if !strings.Contains(out, "persona=realism") {
		t.Errorf("logs missing the persona name:\n%s", out)
	}
	if !strings.Contains(out, "model=m1") || !strings.Contains(out, "model=m2") {
		t.Errorf("logs should show m1 before and m2 after the switch:\n%s", out)
	}
}
