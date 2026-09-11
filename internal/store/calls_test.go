package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/maxdollinger/meeth-councile/internal/llm"
)

func newTestDB(t *testing.T) *Calls {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "calls.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewCalls(db)
}

func TestCallsAppendAndTotals(t *testing.T) {
	ctx := context.Background()
	calls := newTestDB(t)

	in := []Call{
		{Round: 1, Speaker: "THALINDRA", Model: "m1", Purpose: CallSpeak,
			Usage: llm.Usage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120, Cost: 0.001}},
		{Round: 1, Speaker: "MORROW", Model: "m2", Purpose: CallUnderstand,
			Usage: llm.Usage{InputTokens: 50, OutputTokens: 10, TotalTokens: 60, Cost: 0.0005}},
	}
	for _, c := range in {
		if err := calls.Append(ctx, c); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, err := calls.Calls(ctx)
	if err != nil {
		t.Fatalf("Calls: %v", err)
	}
	if len(got) != len(in) {
		t.Fatalf("calls = %d, want %d", len(got), len(in))
	}
	for i := range in {
		if got[i].Speaker != in[i].Speaker || got[i].Purpose != in[i].Purpose ||
			got[i].Usage.Cost != in[i].Usage.Cost {
			t.Errorf("call[%d] = %+v, want %+v", i, got[i], in[i])
		}
		if got[i].CreatedAt.IsZero() {
			t.Errorf("call[%d] has zero CreatedAt", i)
		}
	}

	total, err := calls.Totals(ctx)
	if err != nil {
		t.Fatalf("Totals: %v", err)
	}
	if total.InputTokens != 150 || total.OutputTokens != 30 ||
		total.TotalTokens != 180 || total.Cost != 0.0015 {
		t.Errorf("total = %+v, want 150/30/180/0.0015", total)
	}
}

func TestRecorderSatisfiesInterface(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "recorder.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	r := NewRecorder(db)
	ctx := context.Background()
	if err := r.Append(ctx, Turn{Round: 1, Order: 1, Speaker: "a", Model: "m1", Content: "hi"}); err != nil {
		t.Fatalf("Append turn: %v", err)
	}
	if err := r.AppendCall(ctx, Call{Round: 1, Speaker: "a", Model: "m1", Purpose: CallSpeak,
		Usage: llm.Usage{TotalTokens: 3, Cost: 0.003}}); err != nil {
		t.Fatalf("AppendCall: %v", err)
	}
	total, err := r.Totals(ctx)
	if err != nil {
		t.Fatalf("Totals: %v", err)
	}
	if total.TotalTokens != 3 || total.Cost != 0.003 {
		t.Errorf("total = %+v, want 3/0.003", total)
	}
}
