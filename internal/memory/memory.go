// Package memory gives each debate persona a private, SQLite-backed memory.
//
// Persistence is internal/store; this package is the persona's domain layer
// over it. It never calls a model. A Memory is the only reader and writer of its
// stored entries. AppendAnswer/AppendUnderstanding run through the store, and
// SystemPrompt/History/ComprehensionPrompt assemble what the persona sees. The
// caller runs any model calls.
package memory

import (
	"context"
	"errors"
	"strings"

	"github.com/maxdollinger/meeth-councile/internal/llm"
	"github.com/maxdollinger/meeth-councile/internal/prompts"
	"github.com/maxdollinger/meeth-councile/internal/store"
)

// Memory is one persona's private store for one discussion.
type Memory struct {
	repo          *store.Memory
	id            string
	discussionID  string
	persona       string
	commonPrompt  string
	personaPrompt string
}

// New loads the persona's memory for discussionID, creating it on first use. The
// prompts are snapshotted by the store at creation, so later edits to the prompt
// files do not rewrite an existing memory.
func New(ctx context.Context, repo *store.Memory, discussionID, persona string) (*Memory, error) {
	if repo == nil {
		return nil, errors.New("memory: store is required")
	}
	discussionID = strings.TrimSpace(discussionID)
	persona = strings.TrimSpace(persona)
	if discussionID == "" {
		return nil, errors.New("memory: discussion id is required")
	}
	if persona == "" {
		return nil, errors.New("memory: persona is required")
	}
	personaPrompt, err := prompts.Persona(persona)
	if err != nil {
		return nil, err
	}

	s, err := repo.Load(ctx, discussionID, persona, prompts.Common(), personaPrompt)
	if err != nil {
		return nil, err
	}
	return &Memory{
		repo:          repo,
		id:            s.ID,
		discussionID:  s.DiscussionID,
		persona:       s.Persona,
		commonPrompt:  s.CommonPrompt,
		personaPrompt: s.PersonaPrompt,
	}, nil
}

// AppendAnswer stores one of the persona's own turns. a.Items is the full loop
// from the speaking agent (agent.Result.History); when empty, a single assistant
// message is stored from a.Content.
func (m *Memory) AppendAnswer(ctx context.Context, a Answer) error {
	items := a.Items
	if len(items) == 0 {
		if strings.TrimSpace(a.Content) == "" {
			return errors.New("memory: answer has no content")
		}
		items = llm.Input{llm.Assistant(a.Content)}
	}
	return m.repo.Append(ctx, m.id, store.Entry{
		Kind:    KindAnswer,
		Speaker: m.persona,
		Items:   items,
	})
}

// AppendUnderstanding stores the persona's personalized interpretation of
// something it heard. u.Source is persisted for audit but never replayed.
func (m *Memory) AppendUnderstanding(ctx context.Context, u Understanding) error {
	content := strings.TrimSpace(u.Content)
	if content == "" {
		return errors.New("memory: understanding has no content")
	}
	return m.repo.Append(ctx, m.id, store.Entry{
		Kind:    KindUnderstanding,
		Speaker: u.Speaker,
		Items:   llm.Input{llm.User(label(u.Speaker, content))},
		Source:  u.Source,
	})
}

// Entries returns the persona's memory in the order it was recorded.
func (m *Memory) Entries(ctx context.Context) ([]Entry, error) {
	return m.repo.Entries(ctx, m.id)
}

func label(speaker, content string) string {
	speaker = strings.TrimSpace(speaker)
	if speaker == "" {
		return content
	}
	return speaker + ": " + content
}
