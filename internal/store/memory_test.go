package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/maxdollinger/meeth-councile/internal/llm"
)

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

func openMemoryRepo(t *testing.T) *Memory {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewMemory(db)
}

func TestMemoryLoadSnapshotsPrompts(t *testing.T) {
	ctx := context.Background()
	repo := openMemoryRepo(t)

	first, err := repo.Load(ctx, "d1", "realism", "common-v1", "persona-v1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	second, err := repo.Load(ctx, "d1", "realism", "common-v2", "persona-v2")
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("ID changed: %q then %q", first.ID, second.ID)
	}
	if second.CommonPrompt != "common-v1" || second.PersonaPrompt != "persona-v1" {
		t.Errorf("prompts = %q/%q, want the first snapshot", second.CommonPrompt, second.PersonaPrompt)
	}
}

func TestMemoryLoadRequiresIDs(t *testing.T) {
	repo := openMemoryRepo(t)
	for _, tc := range []struct{ discussion, persona string }{
		{"", "realism"},
		{"d1", ""},
	} {
		if _, err := repo.Load(context.Background(), tc.discussion, tc.persona, "c", "p"); err == nil {
			t.Errorf("Load(%q, %q): want error, got nil", tc.discussion, tc.persona)
		}
	}
}

func TestMemoryAppendAndEntriesRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := openMemoryRepo(t)
	snap, err := repo.Load(ctx, "d1", "realism", "c", "p")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	items := llm.Input{
		llm.Reasoning{ID: "rs_1", EncryptedContent: "enc"},
		llm.FunctionCall{ID: "fc_1", CallID: "call_1", Name: "research_assistant", Arguments: `{"query":"x"}`},
		llm.FunctionCallOutput{CallID: "call_1", Output: "out"},
		llm.Assistant("done"),
	}
	if err := repo.Append(ctx, snap.ID, Entry{Kind: "answer", Speaker: "realism", Items: items, Source: "raw"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := repo.Append(ctx, snap.ID, Entry{Kind: "understanding", Speaker: "error-theory", Items: llm.Input{llm.User("hi")}}); err != nil {
		t.Fatalf("second Append: %v", err)
	}

	entries, err := repo.Entries(ctx, snap.ID)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Seq != 1 || entries[1].Seq != 2 {
		t.Errorf("seq = %d, %d; want 1, 2", entries[0].Seq, entries[1].Seq)
	}
	if entries[0].Kind != "answer" || entries[0].Speaker != "realism" || entries[0].Source != "raw" {
		t.Errorf("entry[0] = %+v, want answer/realism/raw", entries[0])
	}
	if got, want := wireJSON(t, entries[0].Items), wireJSON(t, items); got != want {
		t.Errorf("items = %s, want %s", got, want)
	}
	if entries[1].Source != "" {
		t.Errorf("entry[1].Source = %q, want empty", entries[1].Source)
	}

	// A different memory sees nothing.
	other, err := repo.Load(ctx, "d2", "realism", "c", "p")
	if err != nil {
		t.Fatalf("other Load: %v", err)
	}
	if entries, _ := repo.Entries(ctx, other.ID); len(entries) != 0 {
		t.Errorf("other memory sees %d entries, want 0", len(entries))
	}
}
