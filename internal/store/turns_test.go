package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openTurnsRepo(t *testing.T) *Turns {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "turns.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewTurns(db)
}

func TestTurnsAppendAndEntriesInOrder(t *testing.T) {
	ctx := context.Background()
	repo := openTurnsRepo(t)

	turns := []Turn{
		{Round: 1, Order: 1, Speaker: "realism", Model: "m1", Content: "first"},
		{Round: 1, Order: 2, Speaker: "error-theory", Model: "m2", Content: "second", Passed: true},
		{Round: 2, Order: 1, Speaker: "expressivism", Model: "m1", Content: "third"},
	}
	for _, turn := range turns {
		if err := repo.Append(ctx, turn); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, err := repo.Entries(ctx)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(got) != len(turns) {
		t.Fatalf("turns = %d, want %d", len(got), len(turns))
	}
	for i, want := range turns {
		if got[i].Round != want.Round || got[i].Order != want.Order ||
			got[i].Speaker != want.Speaker || got[i].Model != want.Model ||
			got[i].Content != want.Content || got[i].Passed != want.Passed {
			t.Errorf("turn[%d] = %+v, want %+v", i, got[i], want)
		}
	}
}

func TestSpeakEntriesInInsertionOrder(t *testing.T) {
	ctx := context.Background()
	repo := openTurnsRepo(t)

	want := []SpeakEntry{
		{Round: 0, Speaker: "moderator", Kind: "opening", Content: "topic"},
		{Round: 1, Speaker: "realism", Model: "m1", Kind: "reasoning", Content: "why"},
		{Round: 1, Speaker: "realism", Model: "m1", Kind: "tool_call", Content: "research {}"},
		{Round: 1, Speaker: "realism", Model: "m1", Kind: "message", Content: "my answer"},
	}
	for _, e := range want {
		if err := repo.AppendSpeakEntry(ctx, e); err != nil {
			t.Fatalf("AppendSpeakEntry: %v", err)
		}
	}

	got, err := repo.SpeakEntries(ctx)
	if err != nil {
		t.Fatalf("SpeakEntries: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("entries = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Round != w.Round || got[i].Speaker != w.Speaker ||
			got[i].Model != w.Model || got[i].Kind != w.Kind || got[i].Content != w.Content {
			t.Errorf("entry[%d] = %+v, want %+v", i, got[i], w)
		}
		if got[i].CreatedAt.IsZero() {
			t.Errorf("entry[%d] has zero CreatedAt", i)
		}
	}
}
