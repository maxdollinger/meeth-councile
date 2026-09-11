package memory

import (
	"github.com/maxdollinger/meeth-councile/internal/llm"
	"github.com/maxdollinger/meeth-councile/internal/store"
)

// Entry is a stored memory row. The persistence shape lives in store; the alias
// keeps this package's API free of the storage import at call sites.
type Entry = store.Entry

// Entry kinds, stored in entries.kind.
const (
	KindAnswer        = "answer"
	KindUnderstanding = "understanding"
	KindSummary       = "summary"
)

// Answer is one turn a persona spoke. Name and Content are the exchange
// payload. Items is the persona's private loop — reasoning, tool calls, tool
// outputs, and final message — and is only populated on the speaker's own copy;
// a merely heard Answer leaves it nil.
type Answer struct {
	Name    string
	Content string
	Items   llm.Input
}

// Understanding is a persona's personalized interpretation of something it
// heard. Source is the raw heard answer, kept for audit and never replayed.
// Items is the private loop — reasoning, tool calls, tool outputs, and final
// message — that settled the interpretation; it replays after the heard turn.
type Understanding struct {
	Speaker string
	Content string
	Source  string
	Items   llm.Input
}
