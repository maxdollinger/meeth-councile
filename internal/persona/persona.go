// Package persona wires one debate agent's private memory to the model. It
// comprehends what it hears (Hear) with a single completion and produces its
// own turns (Speak) through an agent loop that may call the research assistant.
//
// A Persona never talks to the database directly; persistence is the memory
// package's job. It is the layer that turns raw exchange payloads into stored
// understandings and answers, using a single completion (understand) to hear
// and a tool loop to speak.
package persona

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/maxdollinger/meeth-councile/internal/agent"
	"github.com/maxdollinger/meeth-councile/internal/llm"
	"github.com/maxdollinger/meeth-councile/internal/logging"
	"github.com/maxdollinger/meeth-councile/internal/memory"
)

const (
	defaultMaxSteps = 8
	researchTool    = "research_assistant"

	// speakInstruction is appended to the persona's history to elicit a turn.
	speakInstruction = "Du bist an der Reihe. Antworte auf die bisherige Diskussion mit deiner eigenen Stimme."
)

// CompletionClient is the slice of *llm.Client this package depends on, kept as
// an interface so tests can substitute a scripted responder.
type CompletionClient interface {
	Response(model string, input llm.Input, opts ...llm.ResponseOption) (llm.Result, error)
}

// Researcher is the slice of *research.Assistant this package depends on. The
// persona's research_assistant tool is a thin adapter over it.
type Researcher interface {
	Research(ctx context.Context, question, caller string) (string, llm.Usage, error)
}

// Persona is one debate agent: a private memory plus the model calls that read
// and write it.
type Persona struct {
	name       string
	memory     *memory.Memory
	client     CompletionClient
	model      string
	researcher Researcher
	maxSteps   int
	opts       []llm.ResponseOption
	loop       *agent.Agent
	logger     *slog.Logger
	baseLogger *slog.Logger
}

// New builds a Persona over mem. client and model drive both the comprehension
// call and the tool loop; researcher answers research_assistant calls. The
// persona's name and system prompt come from its memory.
func New(mem *memory.Memory, client CompletionClient, model string, researcher Researcher, opts ...Option) (*Persona, error) {
	if mem == nil {
		return nil, errors.New("persona: memory is required")
	}
	if client == nil {
		return nil, errors.New("persona: client is required")
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("persona: model is required")
	}
	if researcher == nil {
		return nil, errors.New("persona: researcher is required")
	}

	p := &Persona{
		name:       mem.Persona(),
		memory:     mem,
		client:     client,
		model:      model,
		researcher: researcher,
		maxSteps:   defaultMaxSteps,
		logger:     logging.Discard(),
		baseLogger: logging.Discard(),
	}
	for _, opt := range opts {
		opt(p)
	}
	p.refreshLogger()
	p.buildLoop()
	return p, nil
}

// Name returns the persona's name.
func (p *Persona) Name() string { return p.name }

// UseModel points the persona's comprehension call and tool loop at model for
// subsequent calls. Memory is untouched, so switching the model mid-discussion
// only changes which model speaks from the accumulated understanding.
func (p *Persona) UseModel(model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return errors.New("persona: model is required")
	}
	p.model = model
	p.refreshLogger()
	p.buildLoop()
	return nil
}

// refreshLogger rebuilds the per-persona logger so every record carries the
// persona name and the model currently in use, each exactly once.
func (p *Persona) refreshLogger() {
	p.logger = p.baseLogger.With("persona", p.name, "model", p.model)
}

func (p *Persona) buildLoop() {
	p.loop = agent.New(p.client, p.model, p.memory.SystemPrompt(), p.loopOptions()...)
}

func (p *Persona) loopOptions() []agent.Option {
	loopOpts := make([]agent.Option, 0, len(p.opts)+3)
	loopOpts = append(loopOpts,
		agent.WithTools(p.researchTool()),
		agent.WithMaxSteps(p.maxSteps),
		agent.WithLogger(p.logger),
	)
	if len(p.opts) > 0 {
		loopOpts = append(loopOpts, agent.WithResponseOptions(p.opts...))
	}
	return loopOpts
}

// Hear interprets something the persona heard. name is the speaker and content
// is what they said. A single comprehension completion turns it into the
// persona's own subjective summary, which is stored and returned. Source keeps
// the raw words for audit, and the returned usage carries that call's tokens
// and cost.
func (p *Persona) Hear(ctx context.Context, name, content string) (memory.Understanding, llm.Usage, error) {
	start := time.Now()
	p.logger.Info("hear started", "speaker", name, "heard_chars", len(content))
	summary, usage, err := p.understand(ctx, name, content)
	if err != nil {
		p.logger.Info("hear failed", "speaker", name, "duration", time.Since(start), "err", err)
		return memory.Understanding{}, llm.Usage{}, err
	}

	u := memory.Understanding{
		Speaker: name,
		Content: summary,
		Source:  content,
	}
	if err := p.memory.AppendUnderstanding(ctx, u); err != nil {
		p.logger.Info("hear failed", "speaker", name, "duration", time.Since(start), "err", err)
		return memory.Understanding{}, usage, err
	}
	p.logger.Info("hear finished",
		"speaker", name,
		"heard_chars", len(content),
		"understanding_chars", len(u.Content),
		"total_tokens", usage.TotalTokens,
		"cost", usage.Cost,
		"duration", time.Since(start),
	)
	return u, usage, nil
}

// HearDirect records what was heard verbatim, without the comprehension step
// and without any model call. The moderator's opening uses it, so every persona
// receives the question itself rather than an interpretation of it.
func (p *Persona) HearDirect(ctx context.Context, name, content string) (memory.Understanding, error) {
	u := memory.Understanding{
		Speaker: name,
		Content: content,
		Source:  content,
	}
	if err := p.memory.AppendUnderstanding(ctx, u); err != nil {
		return memory.Understanding{}, err
	}
	p.logger.Info("heard directly", "speaker", name, "chars", len(content))
	return u, nil
}

// Speak produces the persona's next turn. It reasons from its full memory
// through the research-capable loop, persists the answer (with the loop, so
// future turns resume its reasoning), and returns the persona's name, the
// answer text, and the loop's summed usage and cost.
func (p *Persona) Speak(ctx context.Context) (string, string, llm.Usage, llm.Input, error) {
	start := time.Now()
	p.logger.Info("speak started")
	compactUsage, err := p.compact(ctx)
	if err != nil {
		p.logger.Warn("context compaction failed", "err", err)
	}
	history, err := p.memory.History(ctx)
	if err != nil {
		p.logger.Info("speak failed", "duration", time.Since(start), "err", err)
		return "", "", compactUsage, nil, err
	}
	input := make(llm.Input, 0, len(history)+1)
	input = append(input, history...)
	input = append(input, llm.User(speakInstruction))

	res, err := p.loop.Run(ctx, input)
	if err != nil {
		p.logger.Info("speak failed", "duration", time.Since(start), "err", err)
		return "", "", compactUsage, nil, err
	}

	usage := res.Usage
	usage.InputTokens += compactUsage.InputTokens
	usage.OutputTokens += compactUsage.OutputTokens
	usage.TotalTokens += compactUsage.TotalTokens
	usage.Cost += compactUsage.Cost

	content := strings.TrimSpace(res.Text)
	output := newItems(input, res.History)
	if err := p.memory.AppendAnswer(ctx, memory.Answer{
		Name:    p.name,
		Content: content,
		Items:   output,
	}); err != nil {
		p.logger.Info("speak failed", "duration", time.Since(start), "err", err)
		return "", "", usage, nil, err
	}
	p.logger.Info("speak finished",
		"chars", len(content),
		"passed", res.Passed,
		"steps", res.Steps,
		"total_tokens", usage.TotalTokens,
		"cost", usage.Cost,
		"duration", time.Since(start),
	)
	return p.name, content, usage, output, nil
}

// compact folds the oldest part of the persona's memory into a summary when the
// replayed history grows past the budget. The moderator opening is never
// summarized. It returns the usage of the summary call so the caller can bill
// it with the turn it precedes.
func (p *Persona) compact(ctx context.Context) (llm.Usage, error) {
	need, err := p.memory.ShouldCompact(ctx)
	if err != nil || !need {
		return llm.Usage{}, err
	}
	prompt, coversThrough, ok, err := p.memory.SummaryPrompt(ctx)
	if err != nil || !ok {
		return llm.Usage{}, err
	}
	opts := append(append([]llm.ResponseOption{}, p.opts...), llm.WithContext(ctx))
	res, err := p.client.Response(p.model, prompt, opts...)
	if err != nil {
		return llm.Usage{}, err
	}
	summary := strings.TrimSpace(res.Text)
	if summary == "" {
		return res.Usage, errors.New("persona: empty summary")
	}
	if err := p.memory.AppendSummary(ctx, summary, coversThrough); err != nil {
		return res.Usage, err
	}
	p.logger.Info("context compacted", "covers_through", coversThrough, "summary_chars", len(summary), "total_tokens", res.Usage.TotalTokens, "cost", res.Usage.Cost)
	return res.Usage, nil
}

// understand runs the persona's single comprehension completion: one model call
// (no tools) that turns what was heard, plus everything already understood,
// into a subjective summary in the persona's own voice. It returns that call's
// usage and cost alongside the summary.
func (p *Persona) understand(ctx context.Context, name, content string) (string, llm.Usage, error) {
	prompt, err := p.memory.ComprehensionPrompt(ctx, memory.Answer{Name: name, Content: content}, nil)
	if err != nil {
		return "", llm.Usage{}, err
	}
	opts := append(append([]llm.ResponseOption{}, p.opts...), llm.WithContext(ctx))
	res, err := p.client.Response(p.model, prompt, opts...)
	if err != nil {
		return "", llm.Usage{}, err
	}
	p.logger.Debug("persona comprehension", "speaker", name, "chars", len(res.Text))
	return strings.TrimSpace(res.Text), res.Usage, nil
}

// researchTool adapts the researcher to the agent loop. The model supplies a
// self-contained query; the raw answer is returned to the loop and is digested
// by the persona like anything else.
func (p *Persona) researchTool() agent.Tool {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Eine eigenständige Frage. Der Recherche-Assistent hat keine Erinnerung an diese Diskussion, gib also den Kontext mit, den er braucht.",
			},
		},
		"required": []string{"query"},
	}
	return agent.NewTool(researchTool,
		"Beantworte eine eigenständige Recherchefrage mit Websuche und Web-Abruf; liefert eine zusammengefasste Antwort, keine Rohquellen.",
		schema,
		func(ctx context.Context, arguments string) (string, error) {
			var in struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal([]byte(arguments), &in); err != nil {
				return "", fmt.Errorf("persona: decode research arguments: %w", err)
			}
			answer, _, err := p.researcher.Research(ctx, in.Query, p.name)
			return answer, err
		},
	)
}

// newItems returns the part of history produced after input. agent.Run echoes
// the whole input at the head of History, so slicing it off leaves only the new
// loop — reasoning, tool calls, tool outputs, and final message — which is what
// belongs in memory.
func newItems(input, history llm.Input) llm.Input {
	if len(history) <= len(input) {
		return nil
	}
	return history[len(input):]
}
