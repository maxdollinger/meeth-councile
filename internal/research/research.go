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
	"log/slog"
	"strings"
	"time"

	"github.com/maxdollinger/meeth-councile/internal/llm"
	"github.com/maxdollinger/meeth-councile/internal/logging"
	"github.com/maxdollinger/meeth-councile/internal/store"
)

// Server tool wire types. OpenRouter executes both server-side.
const (
	webSearch = "openrouter:web_search"
	webFetch  = "openrouter:web_fetch"
)

// systemPrompt keeps the assistant on task: answer one standalone question from
// web evidence, in its own words, without exposing sources or process.
const systemPrompt = `Du bist ein Recherche-Assistent für eine philosophische Debatte. Beantworte die Frage des Nutzers mit Websuche und Web-Abruf. Die Frage ist in sich geschlossen; du hast keinen weiteren Kontext zur Diskussion.

- Suche, wenn eine aktuelle Tatsache, ein Fall oder ein Beleg weiterhelfen würde. Rufe eine Seite ab, wenn ein Ergebnis es wert scheint, vollständig gelesen zu werden.
- Antworte direkt, in einer kurzen, in sich geschlossenen Zusammenfassung. Sag, was bekannt ist; benenne Uneinigkeit, wo sie relevant ist.
- Schreibe für jemanden, der aus deiner Antwort schließen wird, nicht deine Quellen liest. Liste keine URLs oder rohen Suchergebnisse auf und beschreibe nicht dein Vorgehen.
- Wenn die Belege dünn oder umstritten sind, sag das klar, statt aufzufüllen.`

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
	logger *slog.Logger
}

// New builds an Assistant that answers through model and audits through log.
// WithResponseOptions applies options (temperature, max output tokens, ...) to
// every request.
func New(client CompletionClient, model string, log *store.Log, opts ...Option) *Assistant {
	if client == nil {
		panic("research: client is required")
	}
	if strings.TrimSpace(model) == "" {
		panic("research: model is required")
	}
	if log == nil {
		panic("research: log is required")
	}
	a := &Assistant{client: client, model: model, log: log, logger: logging.Discard()}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Research answers one standalone question and appends the call to the assistant
// log. caller identifies the agent that asked. question is required. It returns
// only the synthesized answer and the usage of the request that produced it.
//
// A failed call is logged too, with the error and no answer. If the audit write
// fails, Research returns that error rather than silently dropping the record.
func (a *Assistant) Research(ctx context.Context, question, caller string) (string, llm.Usage, error) {
	q := strings.TrimSpace(question)
	if q == "" {
		return "", llm.Usage{}, errors.New("research: question is required")
	}

	entry := store.LogEntry{
		Caller:   strings.TrimSpace(caller),
		Question: q,
	}

	opts := make([]llm.ResponseOption, 0, len(a.opts)+2)
	opts = append(opts, a.opts...)
	opts = append(
		opts,
		llm.WithServerTools(
			llm.ServerTool{
				Type:       webSearch,
				Parameters: map[string]any{"max_results": 5},
			},
			llm.ServerTool{Type: webFetch},
		),
		llm.WithContext(ctx),
	)

	start := time.Now()
	a.logger.Info("research call started", "caller", entry.Caller, "model", a.model)
	res, err := a.client.Response(a.model, llm.Input{
		llm.System(systemPrompt),
		llm.User("Frage: " + q),
	}, opts...)
	if err != nil {
		callErr := fmt.Errorf("research: %w", err)
		entry.Err = callErr.Error()
		a.logger.Warn("research call failed", "caller", entry.Caller, "duration", time.Since(start), "err", callErr)
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
	a.logger.Info(
		"research call finished",
		"caller", entry.Caller,
		"chars", len(entry.Answer),
		"input_tokens", res.Usage.InputTokens,
		"output_tokens", res.Usage.OutputTokens,
		"total_tokens", res.Usage.TotalTokens,
		"cost", res.Usage.Cost,
		"duration", time.Since(start),
	)
	return entry.Answer, res.Usage, nil
}
