// Package research implements the debate's research_assistant tool.
//
// It answers one self-contained question using OpenRouter's web search and web
// fetch server tools. The server runs the search/fetch loop itself, so the
// whole tool is a single /responses call that returns only a synthesized
// answer: no raw sources reach the caller.
package research

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/maxdollinger/meeth-councile/internal/llm"
	"github.com/maxdollinger/meeth-councile/internal/store"
)

// Server tool wire types. OpenRouter executes both server-side.
const (
	webSearch = "openrouter:web_search"
	webFetch  = "openrouter:web_fetch"
)

// systemPrompt keeps the assistant on task: answer one standalone question from
// web evidence, in its own words, without exposing sources or process.
const systemPrompt = `You are a research assistant for a philosophy debate. Answer the user's question using web search and web fetch. The question is self-contained; you have no other context about the discussion.

- Search when a current fact, case, or piece of evidence would help. Fetch a page when a result looks worth reading in full.
- Answer directly, in a short, self-contained synthesis. State what is known; note disagreement where it matters.
- Write for someone who will reason from your answer, not read your sources. Do not list URLs or raw search results, and do not describe your process.
- If the evidence is thin or contested, say so plainly instead of padding.`

// CompletionClient is the slice of *llm.Client the assistant depends on, kept
// as an interface so tests can substitute a scripted responder.
type CompletionClient interface {
	Response(model string, input llm.Input, opts ...llm.ResponseOption) (llm.Result, error)
}

// Assistant answers research questions for the debate agents and records every
// call in its audit log.
type Assistant struct {
	client CompletionClient
	model  string
	log    *store.Log
	opts   []llm.ResponseOption
}

// New builds an Assistant that answers through model and audits through log.
// opts are applied to every request (temperature, max output tokens, ...).
func New(client CompletionClient, model string, log *store.Log, opts ...llm.ResponseOption) *Assistant {
	if client == nil {
		panic("research: client is required")
	}
	if strings.TrimSpace(model) == "" {
		panic("research: model is required")
	}
	if log == nil {
		panic("research: log is required")
	}
	return &Assistant{client: client, model: model, log: log, opts: opts}
}

// Research answers one standalone question and appends the call to the assistant
// log. caller identifies the agent that asked. question is required; background
// is optional extra context and may be empty. It returns only the synthesized
// answer and the usage of the request that produced it.
//
// A failed call is logged too, with the error and no answer. If the audit write
// fails, Research returns that error rather than silently dropping the record.
func (a *Assistant) Research(ctx context.Context, question, background, caller string) (string, llm.Usage, error) {
	q := strings.TrimSpace(question)
	if q == "" {
		return "", llm.Usage{}, errors.New("research: question is required")
	}

	entry := store.LogEntry{
		Caller:   strings.TrimSpace(caller),
		Question: q,
		Context:  strings.TrimSpace(background),
	}

	opts := make([]llm.ResponseOption, 0, len(a.opts)+2)
	opts = append(opts, a.opts...)
	opts = append(opts,
		llm.WithServerTools(llm.ServerTool{Type: webSearch}, llm.ServerTool{Type: webFetch}),
		llm.WithContext(ctx),
	)

	res, err := a.client.Response(a.model, llm.Input{
		llm.System(systemPrompt),
		llm.User(prompt(q, background)),
	}, opts...)
	if err != nil {
		callErr := fmt.Errorf("research: %w", err)
		entry.Err = callErr.Error()
		if logErr := a.log.Append(ctx, entry); logErr != nil {
			return "", llm.Usage{}, errors.Join(callErr, logErr)
		}
		return "", llm.Usage{}, callErr
	}

	entry.Answer = strings.TrimSpace(res.Text)
	entry.Usage = res.Usage
	if err := a.log.Append(ctx, entry); err != nil {
		return "", res.Usage, err
	}
	return entry.Answer, res.Usage, nil
}

func prompt(question, background string) string {
	if strings.TrimSpace(background) == "" {
		return "Question: " + question
	}
	return "Question: " + question + "\n\nContext: " + background
}
