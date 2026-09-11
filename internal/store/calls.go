package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/maxdollinger/meeth-councile/internal/llm"
)

// Call purposes, stored in model_calls.purpose.
const (
	CallSpeak      = "speak"
	CallUnderstand = "understand"
)

// Call is one model call made during the debate, with its token usage and cost.
// Round, Speaker, Model, and Purpose place it; Usage holds the tokens and cost.
type Call struct {
	ID        int64
	Round     int
	Speaker   string
	Model     string
	Purpose   string
	Usage     llm.Usage
	CreatedAt time.Time
}

// Calls is the repository for the debate's model-call cost log.
type Calls struct {
	db *sql.DB
}

// NewCalls returns a Calls repository backed by db. It only takes the plain
// database handle; opening the database is Open's job.
func NewCalls(db *sql.DB) *Calls {
	if db == nil {
		panic("store: db is required")
	}
	return &Calls{db: db}
}

// Append records one model call.
func (c *Calls) Append(ctx context.Context, call Call) error {
	createdAt := call.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO model_calls
			(round, speaker, model, purpose, input_tokens, output_tokens, total_tokens, cost, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		call.Round, call.Speaker, call.Model, call.Purpose,
		call.Usage.InputTokens, call.Usage.OutputTokens, call.Usage.TotalTokens, call.Usage.Cost,
		createdAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("store: append model call: %w", err)
	}
	return nil
}

// Calls returns the model calls in insertion order, oldest first.
func (c *Calls) Calls(ctx context.Context) ([]Call, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT id, round, speaker, model, purpose,
		       input_tokens, output_tokens, total_tokens, cost, created_at
		FROM model_calls ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: list model calls: %w", err)
	}
	defer rows.Close()

	var out []Call
	for rows.Next() {
		var (
			call      Call
			createdAt int64
		)
		if err := rows.Scan(
			&call.ID, &call.Round, &call.Speaker, &call.Model, &call.Purpose,
			&call.Usage.InputTokens, &call.Usage.OutputTokens, &call.Usage.TotalTokens, &call.Usage.Cost,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan model call: %w", err)
		}
		call.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, call)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read model calls: %w", err)
	}
	return out, nil
}

// Totals returns the summed usage and cost of every recorded model call.
func (c *Calls) Totals(ctx context.Context) (llm.Usage, error) {
	var u llm.Usage
	err := c.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(total_tokens), 0), COALESCE(SUM(cost), 0)
		FROM model_calls`).Scan(&u.InputTokens, &u.OutputTokens, &u.TotalTokens, &u.Cost)
	if err != nil {
		return llm.Usage{}, fmt.Errorf("store: total model calls: %w", err)
	}
	return u, nil
}

// Recorder is the discussion's durable sink: the shared transcript (turns and
// speak entries) plus the model-call cost log.
type Recorder struct {
	*Turns
	calls *Calls
}

// NewRecorder returns a Recorder backed by db.
func NewRecorder(db *sql.DB) *Recorder {
	return &Recorder{Turns: NewTurns(db), calls: NewCalls(db)}
}

// AppendCall records one model call.
func (r *Recorder) AppendCall(ctx context.Context, call Call) error {
	return r.calls.Append(ctx, call)
}

// Calls returns the recorded model calls.
func (r *Recorder) Calls(ctx context.Context) ([]Call, error) {
	return r.calls.Calls(ctx)
}

// Totals returns the summed usage and cost of every recorded model call.
func (r *Recorder) Totals(ctx context.Context) (llm.Usage, error) {
	return r.calls.Totals(ctx)
}
