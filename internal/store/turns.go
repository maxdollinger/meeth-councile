package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Turns is the repository for the shared discussion transcript.
type Turns struct {
	db *sql.DB
}

// NewTurns returns a Turns repository backed by db. It only takes the plain
// database handle; opening the database is Open's job.
func NewTurns(db *sql.DB) *Turns {
	if db == nil {
		panic("store: db is required")
	}
	return &Turns{db: db}
}

// Turn is one recorded turn of a discussion. Round and Order place it in the
// transcript; Passed marks a PASS turn. Speaker and Content are the simple
// "who said what" record; Model is the model that produced it.
type Turn struct {
	ID        int64
	Round     int
	Order     int
	Speaker   string
	Model     string
	Content   string
	Passed    bool
	CreatedAt time.Time
}

// Append records one turn.
func (t *Turns) Append(ctx context.Context, turn Turn) error {
	_, err := t.db.ExecContext(ctx, `
		INSERT INTO turns (round, turn, speaker, model, content, passed, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		turn.Round, turn.Order, turn.Speaker, turn.Model,
		turn.Content, boolInt(turn.Passed), time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("store: append turn: %w", err)
	}
	return nil
}

// SpeakEntry is one output item produced during a speak turn. Kind is the item
// type ("opening", "reasoning", "tool_call", "tool_output", "message", "pass");
// Content is its rendered text. Round is 0 for the moderator opening.
type SpeakEntry struct {
	ID        int64
	Round     int
	Speaker   string
	Model     string
	Kind      string
	Content   string
	CreatedAt time.Time
}

// AppendSpeakEntry records one speak output.
func (t *Turns) AppendSpeakEntry(ctx context.Context, e SpeakEntry) error {
	createdAt := e.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	_, err := t.db.ExecContext(ctx, `
		INSERT INTO speak_entries (round, speaker, model, kind, content, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		e.Round, e.Speaker, e.Model, e.Kind, e.Content, createdAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("store: append speak entry: %w", err)
	}
	return nil
}

// SpeakEntries returns the speak outputs ordered by time, then by insertion
// order for outputs recorded within the same second.
func (t *Turns) SpeakEntries(ctx context.Context) ([]SpeakEntry, error) {
	rows, err := t.db.QueryContext(ctx, `
		SELECT id, round, speaker, model, kind, content, created_at
		FROM speak_entries ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("store: list speak entries: %w", err)
	}
	defer rows.Close()

	var out []SpeakEntry
	for rows.Next() {
		var (
			e         SpeakEntry
			createdAt int64
		)
		if err := rows.Scan(
			&e.ID, &e.Round, &e.Speaker, &e.Model, &e.Kind, &e.Content, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan speak entry: %w", err)
		}
		e.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read speak entries: %w", err)
	}
	return out, nil
}

// Entries returns the turns in speaking order.
func (t *Turns) Entries(ctx context.Context) ([]Turn, error) {
	rows, err := t.db.QueryContext(ctx, `
		SELECT id, round, turn, speaker, model, content, passed, created_at
		FROM turns ORDER BY round, turn`)
	if err != nil {
		return nil, fmt.Errorf("store: list turns: %w", err)
	}
	defer rows.Close()

	var out []Turn
	for rows.Next() {
		var (
			turn      Turn
			passed    int
			createdAt int64
		)
		if err := rows.Scan(
			&turn.ID, &turn.Round, &turn.Order, &turn.Speaker, &turn.Model,
			&turn.Content, &passed, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan turn: %w", err)
		}
		turn.Passed = passed != 0
		turn.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, turn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read turns: %w", err)
	}
	return out, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
