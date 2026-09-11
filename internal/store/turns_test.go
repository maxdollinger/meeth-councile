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
		if err := repo.Append(ctx, "d1", turn); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, err := repo.Entries(ctx, "d1")
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

	if other, _ := repo.Entries(ctx, "d2"); len(other) != 0 {
		t.Errorf("other discussion sees %d turns, want 0", len(other))
	}
}

func TestTurnsAppendRequiresDiscussionID(t *testing.T) {
	if err := openTurnsRepo(t).Append(context.Background(), "  ", Turn{Speaker: "realism"}); err == nil {
		t.Fatal("blank discussion id: want error, got nil")
	}
}
