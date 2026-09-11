// Package persona wires one debate agent's private memory to the model: it
// comprehends what it hears (Hear) and produces its own turns (Speak), both
// through an agent loop that may call the research assistant.
//
// A Persona never talks to the database directly; persistence is the memory
// package's job. It is the layer that turns raw exchange payloads into stored
// understandings and answers, using a single completion (understand) plus a
// tool loop shared by Hear and Speak.
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
	speakInstruction = "It is your turn. Respond to the discussion so far in your own voice."

	// hearInstruction is appended after the persona's own initial summary of
	// what it heard, inviting it to research and settle on an understanding.
	hearInstruction = "That is your first read on what you just heard. If a specific fact, case, or piece of evidence would sharpen it, use research_assistant. Then state your settled understanding in your own words."
)

// CompletionClient is the slice of *llm.Client this package depends on, kept as
// an interface so tests can substitute a scripted responder.
type CompletionClient interface {
	Response(model string, input llm.Input, opts ...llm.ResponseOption) (llm.Result, error)
}

// Researcher is the slice of *research.Assistant this package depends on. The
// persona's research_assistant tool is a thin adapter over it.
type Researcher interface {
	Research(ctx context.Context, question, background, caller string) (string, llm.Usage, error)
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
	}
	for _, opt := range opts {
		opt(p)
	}
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
	p.buildLoop()
	return nil
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
// is what they said. The raw content is first reduced to the persona's own
// subjective summary (understand), then folded into a research-capable loop
// over the full history. The settled understanding is stored and returned;
// Source keeps the raw words for audit, and Items keeps the loop so future
// turns replay how the persona arrived at it.
func (p *Persona) Hear(ctx context.Context, name, content string) (memory.Understanding, error) {
	start := time.Now()
	p.logger.Info("hear started", "persona", p.name, "speaker", name, "heard_chars", len(content))
	summary, err := p.understand(ctx, name, content)
	if err != nil {
		p.logger.Info("hear failed", "persona", p.name, "speaker", name, "duration", time.Since(start), "err", err)
		return memory.Understanding{}, err
	}

	history, err := p.memory.History(ctx)
	if err != nil {
		p.logger.Info("hear failed", "persona", p.name, "speaker", name, "duration", time.Since(start), "err", err)
		return memory.Understanding{}, err
	}
	input := make(llm.Input, 0, len(history)+2)
	input = append(input, history...)
	input = append(input, llm.Assistant(summary), llm.User(hearInstruction))

	res, err := p.loop.Run(ctx, input)
	if err != nil {
		p.logger.Info("hear failed", "persona", p.name, "speaker", name, "duration", time.Since(start), "err", err)
		return memory.Understanding{}, err
	}

	u := memory.Understanding{
		Speaker: name,
		Content: strings.TrimSpace(res.Text),
		Source:  content,
		Items:   newItems(input, res.History),
	}
	if err := p.memory.AppendUnderstanding(ctx, u); err != nil {
		p.logger.Info("hear failed", "persona", p.name, "speaker", name, "duration", time.Since(start), "err", err)
		return memory.Understanding{}, err
	}
	p.logger.Info("hear finished", "persona", p.name, "speaker", name, "heard_chars", len(content), "understanding_chars", len(u.Content), "duration", time.Since(start))
	return u, nil
}

// Speak produces the persona's next turn. It reasons from its full memory
// through the research-capable loop, persists the answer (with the loop, so
// future turns resume its reasoning), and returns the persona's name and the
// answer text.
func (p *Persona) Speak(ctx context.Context) (string, string, llm.Input, error) {
	start := time.Now()
	p.logger.Info("speak started", "persona", p.name, "model", p.model)
	history, err := p.memory.History(ctx)
	if err != nil {
		p.logger.Info("speak failed", "persona", p.name, "model", p.model, "duration", time.Since(start), "err", err)
		return "", "", nil, err
	}
	input := make(llm.Input, 0, len(history)+1)
	input = append(input, history...)
	input = append(input, llm.User(speakInstruction))

	res, err := p.loop.Run(ctx, input)
	if err != nil {
		p.logger.Info("speak failed", "persona", p.name, "model", p.model, "duration", time.Since(start), "err", err)
		return "", "", nil, err
	}

	content := strings.TrimSpace(res.Text)
	output := newItems(input, res.History)
	if err := p.memory.AppendAnswer(ctx, memory.Answer{
		Name:    p.name,
		Content: content,
		Items:   output,
	}); err != nil {
		p.logger.Info("speak failed", "persona", p.name, "model", p.model, "duration", time.Since(start), "err", err)
		return "", "", nil, err
	}
	p.logger.Info("speak finished", "persona", p.name, "model", p.model, "chars", len(content), "passed", res.Passed, "steps", res.Steps, "duration", time.Since(start))
	return p.name, content, output, nil
}

// understand runs the persona's single comprehension completion: one model call
// (no tools) that turns what was heard, plus everything already understood,
// into a subjective summary in the persona's own voice.
func (p *Persona) understand(ctx context.Context, name, content string) (string, error) {
	prompt, err := p.memory.ComprehensionPrompt(ctx, memory.Answer{Name: name, Content: content}, nil)
	if err != nil {
		return "", err
	}
	opts := append(append([]llm.ResponseOption{}, p.opts...), llm.WithContext(ctx))
	res, err := p.client.Response(p.model, prompt, opts...)
	if err != nil {
		return "", err
	}
	p.logger.Debug("persona comprehension", "speaker", name, "model", p.model, "chars", len(res.Text))
	return strings.TrimSpace(res.Text), nil
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
				"description": "A self-contained question. The research assistant has no memory of this discussion, so include whatever context it needs.",
			},
		},
		"required": []string{"query"},
	}
	return agent.NewTool(researchTool,
		"Answer a self-contained research question using web search and fetch; returns a synthesized answer, not raw sources.",
		schema,
		func(ctx context.Context, arguments string) (string, error) {
			var in struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal([]byte(arguments), &in); err != nil {
				return "", fmt.Errorf("persona: decode research arguments: %w", err)
			}
			answer, _, err := p.researcher.Research(ctx, in.Query, "", p.name)
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
