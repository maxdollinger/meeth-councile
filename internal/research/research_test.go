package research

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxdollinger/meeth-councile/internal/llm"
	"github.com/maxdollinger/meeth-councile/internal/store"
)

type fakeCall struct {
	model string
	input llm.Input
	opts  []llm.ResponseOption
}

type fakeClient struct {
	calls   []fakeCall
	respond func(call fakeCall) (llm.Result, error)
}

func (f *fakeClient) Response(model string, input llm.Input, opts ...llm.ResponseOption) (llm.Result, error) {
	call := fakeCall{model: model, input: input, opts: opts}
	f.calls = append(f.calls, call)
	if f.respond == nil {
		return llm.Result{}, nil
	}
	return f.respond(call)
}

func openLog(t *testing.T) *store.Log {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "research.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return store.NewLog(db)
}

func TestNewPanicsOnNilClient(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New with nil client did not panic")
		}
	}()
	New(nil, "m", openLog(t))
}

func TestNewPanicsOnEmptyModel(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New with empty model did not panic")
		}
	}()
	New(&fakeClient{}, "  ", openLog(t))
}

func TestNewPanicsOnNilLog(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New with nil log did not panic")
		}
	}()
	New(&fakeClient{}, "m", nil)
}

func TestResearchReturnsAnswerAndUsage(t *testing.T) {
	wantUsage := llm.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30, Cost: 0.004}
	fc := &fakeClient{respond: func(fakeCall) (llm.Result, error) {
		return llm.Result{Text: "  the synthesized answer\n", Usage: wantUsage}, nil
	}}

	got, usage, err := New(fc, "research-model", openLog(t)).Research(
		context.Background(), "What is the evidence?", "realism",
	)
	if err != nil {
		t.Fatalf("Research returned error: %v", err)
	}
	if got != "the synthesized answer" {
		t.Errorf("answer = %q, want trimmed synthesis", got)
	}
	if usage != wantUsage {
		t.Errorf("usage = %+v, want %+v", usage, wantUsage)
	}

	call := fc.calls[0]
	if call.model != "research-model" {
		t.Errorf("model = %q, want research-model", call.model)
	}
	if len(call.input) != 2 {
		t.Fatalf("input = %+v, want system + user", call.input)
	}
	sys, ok := call.input[0].(llm.Message)
	if !ok || sys.Role != llm.RoleSystem || sys.Content == "" {
		t.Errorf("input[0] = %+v, want a non-empty system message", call.input[0])
	}
	user, ok := call.input[1].(llm.Message)
	if !ok || user.Role != llm.RoleUser {
		t.Fatalf("input[1] = %+v, want a user message", call.input[1])
	}
	if !strings.Contains(user.Content, "What is the evidence?") {
		t.Errorf("user content = %q, want the question", user.Content)
	}
}

func TestResearchLogsCall(t *testing.T) {
	usage := llm.Usage{InputTokens: 3, OutputTokens: 4, TotalTokens: 7, Cost: 0.01}
	fc := &fakeClient{respond: func(fakeCall) (llm.Result, error) {
		return llm.Result{Text: "answer", Usage: usage}, nil
	}}
	log := openLog(t)

	if _, _, err := New(fc, "m", log).Research(context.Background(), "Q?", "error-theory"); err != nil {
		t.Fatalf("Research returned error: %v", err)
	}

	entries, err := log.Entries(context.Background())
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Caller != "error-theory" || e.Question != "Q?" || e.Answer != "answer" {
		t.Errorf("entry = %+v, want caller/question/answer recorded", e)
	}
	if e.Usage != usage {
		t.Errorf("entry usage = %+v, want %+v", e.Usage, usage)
	}
	if e.Err != "" {
		t.Errorf("entry error = %q, want empty", e.Err)
	}
}

func TestResearchLogsFailure(t *testing.T) {
	fc := &fakeClient{respond: func(fakeCall) (llm.Result, error) {
		return llm.Result{}, errors.New("upstream down")
	}}
	log := openLog(t)

	if _, _, err := New(fc, "m", log).Research(context.Background(), "Q?", "realism"); err == nil {
		t.Fatal("expected error, got nil")
	}

	entries, err := log.Entries(context.Background())
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 (failures logged too)", len(entries))
	}
	e := entries[0]
	if e.Answer != "" {
		t.Errorf("answer = %q, want empty on failure", e.Answer)
	}
	if !strings.Contains(e.Err, "upstream down") {
		t.Errorf("error = %q, want the failure recorded", e.Err)
	}
}

func TestResearchRequiresQuestion(t *testing.T) {
	fc := &fakeClient{}
	log := openLog(t)
	if _, _, err := New(fc, "m", log).Research(context.Background(), "   ", "caller"); err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(fc.calls) != 0 {
		t.Errorf("model called %d times, want 0 for an empty question", len(fc.calls))
	}
	entries, err := log.Entries(context.Background())
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries = %d, want 0 for a rejected question", len(entries))
	}
}

func TestResearchWrapsClientError(t *testing.T) {
	fc := &fakeClient{respond: func(fakeCall) (llm.Result, error) {
		return llm.Result{}, errors.New("upstream down")
	}}
	_, _, err := New(fc, "m", openLog(t)).Research(context.Background(), "Q?", "caller")
	if err == nil || !strings.Contains(err.Error(), "upstream down") {
		t.Fatalf("error = %v, want the client error", err)
	}
}
