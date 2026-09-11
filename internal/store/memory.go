package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/maxdollinger/meeth-councile/internal/llm"
)

// Memory is the repository for persona memories and their entries.
type Memory struct {
	db *sql.DB
}

// NewMemory returns a Memory repository backed by db. It only takes the plain
// database handle; opening the database is Open's job.
func NewMemory(db *sql.DB) *Memory {
	if db == nil {
		panic("store: db is required")
	}
	return &Memory{db: db}
}

// Snapshot is the identity and snapshotted system-prompt pieces of one persona's
// memory. Prompts are captured when the memory is first created, so later edits
// to the prompt files do not rewrite an existing memory.
type Snapshot struct {
	ID            string
	Persona       string
	CommonPrompt  string
	PersonaPrompt string
}

// Entry is one stored unit of a persona's memory, ordered by Seq. Content is
// the persona's own text (its answer or understanding summary); Items is the
// full agent loop, kept for audit and never replayed into history.
type Entry struct {
	Seq       int
	Kind      string
	Speaker   string
	Content   string
	Items     llm.Input
	Source    string
	CreatedAt time.Time
}

// Load creates the persona's memory on first use, snapshotting the prompts, and
// returns it. On later calls with the same persona it returns the stored
// snapshot, ignoring the prompts passed in.
func (m *Memory) Load(ctx context.Context, persona, commonPrompt, personaPrompt string) (Snapshot, error) {
	persona = strings.TrimSpace(persona)
	if persona == "" {
		return Snapshot{}, errors.New("store: persona is required")
	}

	s := Snapshot{
		ID:            persona,
		Persona:       persona,
		CommonPrompt:  commonPrompt,
		PersonaPrompt: personaPrompt,
	}
	if _, err := m.db.ExecContext(ctx, `
		INSERT INTO memories (persona, common_prompt, persona_prompt, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (persona) DO NOTHING`,
		persona, commonPrompt, personaPrompt, time.Now().Unix(),
	); err != nil {
		return Snapshot{}, fmt.Errorf("store: create memory: %w", err)
	}

	row := m.db.QueryRowContext(ctx,
		`SELECT persona, common_prompt, persona_prompt
		 FROM memories WHERE persona = ?`,
		persona)
	if err := row.Scan(&s.Persona, &s.CommonPrompt, &s.PersonaPrompt); err != nil {
		return Snapshot{}, fmt.Errorf("store: load memory: %w", err)
	}
	s.ID = s.Persona
	return s, nil
}

// Append stores one entry under memoryID, assigning the next sequence number.
func (m *Memory) Append(ctx context.Context, memoryID string, e Entry) error {
	raw, err := marshalItems(e.Items)
	if err != nil {
		return err
	}

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	var seq int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM entries WHERE memory_id = ?`, memoryID,
	).Scan(&seq); err != nil {
		return fmt.Errorf("store: next seq: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO entries (memory_id, seq, kind, speaker, content, items, source, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		memoryID, seq, e.Kind, e.Speaker, e.Content, raw, nullString(e.Source), time.Now().Unix(),
	); err != nil {
		return fmt.Errorf("store: append entry: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// Entries returns memoryID's entries in the order they were recorded.
func (m *Memory) Entries(ctx context.Context, memoryID string) ([]Entry, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT seq, kind, speaker, content, items, COALESCE(source, ''), created_at
		FROM entries WHERE memory_id = ? ORDER BY seq`, memoryID)
	if err != nil {
		return nil, fmt.Errorf("store: list entries: %w", err)
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		var (
			e         Entry
			raw       string
			createdAt int64
		)
		if err := rows.Scan(&e.Seq, &e.Kind, &e.Speaker, &e.Content, &raw, &e.Source, &createdAt); err != nil {
			return nil, fmt.Errorf("store: scan entry: %w", err)
		}
		items, err := unmarshalItems(raw)
		if err != nil {
			return nil, err
		}
		e.Items = items
		e.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read entries: %w", err)
	}
	return out, nil
}

func marshalItems(items llm.Input) (string, error) {
	wire := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		wire = append(wire, item.ResponseItem())
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		return "", fmt.Errorf("store: marshal items: %w", err)
	}
	return string(raw), nil
}

func unmarshalItems(raw string) (llm.Input, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var wire []map[string]any
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		return nil, fmt.Errorf("store: unmarshal items: %w", err)
	}
	in := make(llm.Input, 0, len(wire))
	for _, item := range wire {
		in = append(in, wireItem(item))
	}
	return in, nil
}

// wireItem lets a stored item round-trip through JSON without recording its
// concrete type. It satisfies llm.Item by echoing the wire map verbatim, so a
// turn saved by one run replays byte-for-byte on the next.
type wireItem map[string]any

func (w wireItem) Type() string {
	s, _ := w["type"].(string)
	return s
}

func (w wireItem) ResponseItem() map[string]any { return map[string]any(w) }
