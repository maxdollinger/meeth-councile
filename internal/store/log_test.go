package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/maxdollinger/meeth-councile/internal/llm"
)

func TestLogAppendEntriesOldestFirst(t *testing.T) {
	ctx := context.Background()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	log := NewLog(db)

	usage := llm.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3, Cost: 0.02}
	if err := log.Append(ctx, LogEntry{Caller: "realism", Question: "Q1", Answer: "A1", Usage: usage}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := log.Append(ctx, LogEntry{Caller: "error-theory", Question: "Q2", Err: "boom"}); err != nil {
		t.Fatalf("second Append: %v", err)
	}

	entries, err := log.Entries(ctx)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	first := entries[0]
	if first.Caller != "realism" || first.Question != "Q1" || first.Answer != "A1" {
		t.Errorf("first = %+v, want fields preserved", first)
	}
	if first.Usage != usage {
		t.Errorf("usage = %+v, want %+v", first.Usage, usage)
	}
	if first.Err != "" {
		t.Errorf("first.Err = %q, want empty", first.Err)
	}
	second := entries[1]
	if second.Answer != "" || second.Err != "boom" {
		t.Errorf("second = %+v, want a failed call with no answer", second)
	}
}

func TestLogPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "research.db")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := NewLog(first).Append(ctx, LogEntry{Caller: "realism", Question: "Q?", Answer: "answer"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	first.Close()

	second, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer second.Close()

	entries, err := NewLog(second).Entries(ctx)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 || entries[0].Answer != "answer" {
		t.Fatalf("entries = %+v, want one preserved entry", entries)
	}
	if entries[0].CreatedAt.IsZero() || time.Since(entries[0].CreatedAt) > time.Minute {
		t.Errorf("CreatedAt = %v, want a recent timestamp", entries[0].CreatedAt)
	}
}
