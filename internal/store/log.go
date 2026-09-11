package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/maxdollinger/meeth-councile/internal/llm"
)

// Log is the repository for the research assistant's audit trail.
type Log struct {
	db *sql.DB
}

// NewLog returns a Log repository backed by db. It only takes the plain
// database handle; opening the database is Open's job.
func NewLog(db *sql.DB) *Log {
	if db == nil {
		panic("store: db is required")
	}
	return &Log{db: db}
}

// LogEntry is one logged research call. Err is set, and Answer empty, when the
// call failed.
type LogEntry struct {
	ID        int64
	Caller    string
	Question  string
	Answer    string
	Usage     llm.Usage
	Err       string
	CreatedAt time.Time
}

// Append records one research call.
func (l *Log) Append(ctx context.Context, e LogEntry) error {
	_, err := l.db.ExecContext(ctx, `
		INSERT INTO research_log
			(caller, question, answer, input_tokens, output_tokens, total_tokens, cost, error, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Caller, e.Question, nullString(e.Answer),
		e.Usage.InputTokens, e.Usage.OutputTokens, e.Usage.TotalTokens, e.Usage.Cost,
		nullString(e.Err), time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("store: write research log: %w", err)
	}
	return nil
}

// Entries returns the log in insertion order, oldest first.
func (l *Log) Entries(ctx context.Context) ([]LogEntry, error) {
	rows, err := l.db.QueryContext(ctx, `
		SELECT id, caller, question, COALESCE(answer, ''),
		       input_tokens, output_tokens, total_tokens, cost, COALESCE(error, ''), created_at
		FROM research_log ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: list research log: %w", err)
	}
	defer rows.Close()

	var out []LogEntry
	for rows.Next() {
		var (
			e         LogEntry
			createdAt int64
		)
		if err := rows.Scan(
			&e.ID, &e.Caller, &e.Question, &e.Answer,
			&e.Usage.InputTokens, &e.Usage.OutputTokens, &e.Usage.TotalTokens, &e.Usage.Cost,
			&e.Err, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan research log: %w", err)
		}
		e.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read research log: %w", err)
	}
	return out, nil
}
