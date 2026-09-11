package memory

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/maxdollinger/meeth-councile/internal/llm"
	"github.com/maxdollinger/meeth-councile/internal/prompts"
	"github.com/maxdollinger/meeth-councile/internal/store"
)

func openRepo(t *testing.T) *store.Memory {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return store.NewMemory(db)
}

func newMemory(t *testing.T, repo *store.Memory, persona string) *Memory {
	t.Helper()
	m, err := New(context.Background(), repo, persona)
	if err != nil {
		t.Fatalf("New(%s): %v", persona, err)
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

func wireOf(in llm.Input) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, item := range in {
		out = append(out, item.ResponseItem())
	}
	return out
}

func TestNewRequiresStore(t *testing.T) {
	if _, err := New(context.Background(), nil, "realism"); err == nil {
		t.Fatal("nil store: want error, got nil")
	}
}

func TestMemoryPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "memory.db")

	first, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	m := newMemory(t, store.NewMemory(first), "realism")
	if err := m.AppendAnswer(ctx, Answer{Name: "realism", Content: "moral facts exist"}); err != nil {
		t.Fatalf("AppendAnswer: %v", err)
	}
	if err := m.AppendUnderstanding(ctx, Understanding{Speaker: "error-theory", Content: "they deny it", Source: "there are no moral facts"}); err != nil {
		t.Fatalf("AppendUnderstanding: %v", err)
	}
	first.Close()

	second, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()
	reloaded := newMemory(t, store.NewMemory(second), "realism")
	entries, err := reloaded.Entries(ctx)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Kind != KindAnswer || entries[1].Kind != KindUnderstanding {
		t.Errorf("kinds = %q, %q; want answer, understanding", entries[0].Kind, entries[1].Kind)
	}
}

func TestMemoryGetOrCreateAndIsolation(t *testing.T) {
	ctx := context.Background()
	repo := openRepo(t)

	first := newMemory(t, repo, "realism")
	second := newMemory(t, repo, "realism")
	if err := first.AppendAnswer(ctx, Answer{Name: "realism", Content: "one"}); err != nil {
		t.Fatalf("AppendAnswer: %v", err)
	}
	entries, err := second.Entries(ctx)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("same persona sees %d entries, want 1", len(entries))
	}

	other := newMemory(t, repo, "expressivism")
	if entries, _ := other.Entries(ctx); len(entries) != 0 {
		t.Errorf("other persona sees %d entries, want 0", len(entries))
	}
}

func TestMemoryRejectsUnknownPersona(t *testing.T) {
	if _, err := New(context.Background(), openRepo(t), "nihilism"); err == nil {
		t.Fatal("unknown persona: want error, got nil")
	}
}

func TestSystemPromptJoinsCommonAndPersona(t *testing.T) {
	m := newMemory(t, openRepo(t), "realism")
	persona, err := prompts.Persona("realism")
	if err != nil {
		t.Fatalf("Persona: %v", err)
	}
	got := m.SystemPrompt()
	if !strings.Contains(got, prompts.Common()) {
		t.Error("SystemPrompt missing common prompt")
	}
	if !strings.Contains(got, persona) {
		t.Error("SystemPrompt missing persona prompt")
	}
	if got == prompts.Common() {
		t.Error("SystemPrompt is only the common prompt")
	}
}

func TestAppendAnswerStoresFullTurn(t *testing.T) {
	ctx := context.Background()
	m := newMemory(t, openRepo(t), "realism")

	reasoning := llm.Reasoning{
		ID:               "rs_1",
		Summary:          []llm.ReasoningSummary{{Text: "weigh it"}},
		EncryptedContent: "enc_abc",
	}
	call := llm.FunctionCall{ID: "fc_1", CallID: "call_1", Name: "research_assistant", Arguments: `{"query":"cases"}`}
	output := llm.FunctionCallOutput{CallID: "call_1", Output: "the understanding"}
	turn := llm.Input{reasoning, call, output, llm.Assistant("my answer")}

	if err := m.AppendAnswer(ctx, Answer{Name: "realism", Content: "my answer", Items: turn}); err != nil {
		t.Fatalf("AppendAnswer: %v", err)
	}
	entries, err := m.Entries(ctx)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Kind != KindAnswer || entries[0].Speaker != "realism" {
		t.Errorf("entry = %+v, want answer by realism", entries[0])
	}
	if got, want := wireJSON(t, entries[0].Items), wireJSON(t, turn); got != want {
		t.Errorf("items = %s, want %s", got, want)
	}
}

func TestAppendAnswerFallsBackToContent(t *testing.T) {
	ctx := context.Background()
	m := newMemory(t, openRepo(t), "realism")

	if err := m.AppendAnswer(ctx, Answer{Name: "realism", Content: "just words"}); err != nil {
		t.Fatalf("AppendAnswer: %v", err)
	}
	entries, _ := m.Entries(ctx)
	want := llm.Input{llm.Assistant("just words")}
	if got, wantJSON := wireJSON(t, entries[0].Items), wireJSON(t, want); got != wantJSON {
		t.Errorf("items = %s, want %s", got, wantJSON)
	}
}

func TestAppendAnswerRejectsEmpty(t *testing.T) {
	m := newMemory(t, openRepo(t), "realism")
	if err := m.AppendAnswer(context.Background(), Answer{Name: "realism"}); err == nil {
		t.Fatal("empty answer: want error, got nil")
	}
}

func TestAppendUnderstandingTagsSpeakerAndKeepsSource(t *testing.T) {
	ctx := context.Background()
	m := newMemory(t, openRepo(t), "realism")

	if err := m.AppendUnderstanding(ctx, Understanding{
		Speaker: "error-theory",
		Content: "they think all moral claims are false",
		Source:  "raw claim",
	}); err != nil {
		t.Fatalf("AppendUnderstanding: %v", err)
	}
	entries, _ := m.Entries(ctx)
	if entries[0].Kind != KindUnderstanding || entries[0].Speaker != "error-theory" {
		t.Errorf("entry = %+v, want understanding by error-theory", entries[0])
	}
	if entries[0].Source != "raw claim" {
		t.Errorf("Source = %q, want raw kept", entries[0].Source)
	}
	want := llm.Input{llm.User("error-theory: they think all moral claims are false")}
	if got, wantJSON := wireJSON(t, entries[0].Items), wireJSON(t, want); got != wantJSON {
		t.Errorf("items = %s, want %s", got, wantJSON)
	}
}

func TestHistoryOrdersEntriesWithoutSystemOrSource(t *testing.T) {
	ctx := context.Background()
	m := newMemory(t, openRepo(t), "realism")

	if err := m.AppendAnswer(ctx, Answer{Name: "realism", Content: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := m.AppendUnderstanding(ctx, Understanding{Speaker: "expressivism", Content: "second", Source: "raw-secret"}); err != nil {
		t.Fatal(err)
	}
	if err := m.AppendAnswer(ctx, Answer{Name: "realism", Content: "third"}); err != nil {
		t.Fatal(err)
	}

	history, err := m.History(ctx)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	want := llm.Input{
		llm.Assistant("first"),
		llm.User("expressivism: second"),
		llm.Assistant("third"),
	}
	if got, wantJSON := wireJSON(t, history), wireJSON(t, want); got != wantJSON {
		t.Errorf("History = %s, want %s", got, wantJSON)
	}
	for _, item := range history {
		wire := item.ResponseItem()
		if wire["role"] == string(llm.RoleSystem) {
			t.Error("History should not contain a system message")
		}
		if s, _ := wire["content"].(string); strings.Contains(s, "raw-secret") {
			t.Error("History leaked understanding Source")
		}
	}
}

func TestHistoryReplaysTextOnlyFromStoredLoops(t *testing.T) {
	ctx := context.Background()
	m := newMemory(t, openRepo(t), "realism")

	loop := llm.Input{
		llm.Reasoning{ID: "rs_1", Summary: []llm.ReasoningSummary{{Text: "secret reasoning"}}},
		llm.FunctionCall{CallID: "call_1", Name: "research_assistant", Arguments: `{"query":"secret query"}`},
		llm.FunctionCallOutput{CallID: "call_1", Output: "secret output"},
		llm.Assistant("answer text"),
	}
	if err := m.AppendAnswer(ctx, Answer{Name: "realism", Content: "answer text", Items: loop}); err != nil {
		t.Fatal(err)
	}
	if err := m.AppendUnderstanding(ctx, Understanding{
		Speaker: "error-theory",
		Content: "their point",
		Source:  "raw secret source",
		Items:   loop,
	}); err != nil {
		t.Fatal(err)
	}

	history, err := m.History(ctx)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	want := llm.Input{
		llm.Assistant("answer text"),
		llm.User("error-theory: their point"),
	}
	if got, wantJSON := wireJSON(t, history), wireJSON(t, want); got != wantJSON {
		t.Errorf("History = %s, want %s", got, wantJSON)
	}
	for _, item := range history {
		if item.Type() != "message" {
			t.Errorf("History item type = %q, want message only", item.Type())
		}
		if s, _ := item.ResponseItem()["content"].(string); strings.Contains(s, "secret") {
			t.Errorf("History leaked loop/source content: %q", s)
		}
	}
}

func TestComprehensionPromptIncludesHistoryTurnAndAnswer(t *testing.T) {
	ctx := context.Background()
	m := newMemory(t, openRepo(t), "realism")

	if err := m.AppendAnswer(ctx, Answer{Name: "realism", Content: "my earlier point"}); err != nil {
		t.Fatal(err)
	}
	turn := llm.Input{
		llm.Assistant("i should check this"),
		llm.FunctionCall{CallID: "call_9", Name: "research_assistant", Arguments: `{"query":"cross-cultural evidence"}`},
	}

	prompt, err := m.ComprehensionPrompt(ctx, Answer{Name: "research_assistant", Content: "some findings"}, turn)
	if err != nil {
		t.Fatalf("ComprehensionPrompt: %v", err)
	}
	if len(prompt) == 0 {
		t.Fatal("empty prompt")
	}
	head, ok := prompt[0].(llm.Message)
	if !ok || head.Role != llm.RoleSystem || !strings.Contains(head.Content, prompts.Common()) {
		t.Fatalf("prompt[0] = %#v, want the system prompt", prompt[0])
	}

	all := wireOf(prompt)
	if !containsContent(all, "my earlier point") {
		t.Error("prompt missing history")
	}
	if !containsContent(all, "cross-cultural evidence") {
		t.Error("prompt missing the live turn")
	}
	if !containsContent(all, "some findings") {
		t.Error("prompt missing the incoming answer")
	}
	last := all[len(all)-1]
	if last["content"] != comprehensionInstruction {
		t.Errorf("last item = %v, want the comprehension instruction", last["content"])
	}
}

func containsContent(items []map[string]any, want string) bool {
	for _, item := range items {
		if s, ok := item["content"].(string); ok && strings.Contains(s, want) {
			return true
		}
		for _, key := range []string{"arguments", "output"} {
			if s, ok := item[key].(string); ok && strings.Contains(s, want) {
				return true
			}
		}
	}
	return false
}

const originalQuestion = "ORIGINAL_QUESTION_MARKER: what makes a moral claim true?"

// fillHistory appends enough large entries to push the replayed history past
// the compaction budget.
func fillHistory(t *testing.T, m *Memory, marker string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		content := marker + "-" + strconv.Itoa(i) + " " + strings.Repeat("wort ", 1600)
		if err := m.AppendAnswer(ctx, Answer{Name: "realism", Content: content}); err != nil {
			t.Fatalf("AppendAnswer: %v", err)
		}
	}
}

func TestShouldCompactTracksBudget(t *testing.T) {
	ctx := context.Background()
	m := newMemory(t, openRepo(t), "realism")
	if err := m.AppendUnderstanding(ctx, Understanding{Speaker: "moderator", Content: originalQuestion, Source: originalQuestion}); err != nil {
		t.Fatal(err)
	}
	if need, err := m.ShouldCompact(ctx); err != nil || need {
		t.Fatalf("ShouldCompact on a tiny memory = %v, %v; want false, nil", need, err)
	}
	fillHistory(t, m, "OLD", 8)
	if need, err := m.ShouldCompact(ctx); err != nil || !need {
		t.Fatalf("ShouldCompact after filling = %v, %v; want true, nil", need, err)
	}
}

func TestSummaryPromptExcludesOpeningAndCoversNewest(t *testing.T) {
	ctx := context.Background()
	m := newMemory(t, openRepo(t), "realism")
	if err := m.AppendUnderstanding(ctx, Understanding{Speaker: "moderator", Content: originalQuestion, Source: originalQuestion}); err != nil {
		t.Fatal(err)
	}
	fillHistory(t, m, "OLD", 8)

	prompt, covers, ok, err := m.SummaryPrompt(ctx)
	if err != nil {
		t.Fatalf("SummaryPrompt: %v", err)
	}
	if !ok {
		t.Fatal("SummaryPrompt ok = false, want true")
	}
	if covers <= 0 {
		t.Errorf("coversThrough = %d, want > 0", covers)
	}
	if containsContent(wireOf(prompt), "ORIGINAL_QUESTION_MARKER") {
		t.Error("summary prompt leaked the original question")
	}
	if !containsContent(wireOf(prompt), "OLD-") {
		t.Error("summary prompt is missing the old entries to summarize")
	}

	// The newest entries stay verbatim: covers must leave the last entry out.
	entries, _ := m.Entries(ctx)
	if covers >= entries[len(entries)-1].Seq {
		t.Errorf("coversThrough = %d, must be below the newest entry %d", covers, entries[len(entries)-1].Seq)
	}
}

func TestAppendSummaryReplacesCoveredEntriesButKeepsOpening(t *testing.T) {
	ctx := context.Background()
	m := newMemory(t, openRepo(t), "realism")
	if err := m.AppendUnderstanding(ctx, Understanding{Speaker: "moderator", Content: originalQuestion, Source: originalQuestion}); err != nil {
		t.Fatal(err)
	}
	fillHistory(t, m, "OLD", 8)

	_, covers, ok, err := m.SummaryPrompt(ctx)
	if err != nil || !ok {
		t.Fatalf("SummaryPrompt = ok %v, err %v", ok, err)
	}
	if err := m.AppendSummary(ctx, "CONDENSED SUMMARY", covers); err != nil {
		t.Fatalf("AppendSummary: %v", err)
	}

	history, err := m.History(ctx)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	want := wireOf(history)
	if len(want) == 0 {
		t.Fatal("empty history")
	}
	if !containsContent(want, "ORIGINAL_QUESTION_MARKER") {
		t.Error("history dropped the original question")
	}
	if !containsContent(want, "CONDENSED SUMMARY") {
		t.Error("history missing the summary")
	}

	entries, _ := m.Entries(ctx)
	// No covered entry may survive in the replayed history.
	for _, e := range entries {
		if e.Seq > covers || e.Kind == KindSummary || isOpening(e) {
			continue
		}
		if containsContent(want, strings.TrimSpace(e.Content)) {
			t.Errorf("covered entry seq %d leaked into history", e.Seq)
		}
	}
}

func TestSummaryPromptFoldsPreviousSummary(t *testing.T) {
	ctx := context.Background()
	m := newMemory(t, openRepo(t), "realism")
	if err := m.AppendUnderstanding(ctx, Understanding{Speaker: "moderator", Content: originalQuestion, Source: originalQuestion}); err != nil {
		t.Fatal(err)
	}
	fillHistory(t, m, "FIRST", 8)
	_, covers, ok, err := m.SummaryPrompt(ctx)
	if err != nil || !ok {
		t.Fatalf("first SummaryPrompt = ok %v, err %v", ok, err)
	}
	if err := m.AppendSummary(ctx, "FIRST SUMMARY", covers); err != nil {
		t.Fatal(err)
	}

	fillHistory(t, m, "SECOND", 8)
	prompt, _, ok, err := m.SummaryPrompt(ctx)
	if err != nil || !ok {
		t.Fatalf("second SummaryPrompt = ok %v, err %v", ok, err)
	}
	all := wireOf(prompt)
	if !containsContent(all, "FIRST SUMMARY") {
		t.Error("second summary prompt did not carry the previous summary")
	}
	if containsContent(all, "ORIGINAL_QUESTION_MARKER") {
		t.Error("second summary prompt leaked the original question")
	}
}
