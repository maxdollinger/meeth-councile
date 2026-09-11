// Package discussion runs the round-based debate: each round every persona
// speaks once in a freshly shuffled order on a randomly assigned model, hears
// the others, and the discussion ends when a whole round passes.
package discussion

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	"github.com/maxdollinger/meeth-councile/internal/agent"
	"github.com/maxdollinger/meeth-councile/internal/llm"
	"github.com/maxdollinger/meeth-councile/internal/logging"
	"github.com/maxdollinger/meeth-councile/internal/memory"
	"github.com/maxdollinger/meeth-councile/internal/store"
)

// Speak output kinds, stored in speak_entries.kind.
const (
	kindOpening = "opening"
	kindMessage = "message"
	kindPass    = "pass"
)

// moderator is the speaker name of the opening topic turn.
const moderator = "moderator"

const defaultMaxRounds = 20

// Turn is one recorded discussion turn.
type Turn = store.Turn

// SpeakEntry is one output item produced during a speak turn.
type SpeakEntry = store.SpeakEntry

// Call is one recorded model call with its tokens and cost.
type Call = store.Call

// Store persists the shared transcript, every speak output, and the model-call
// cost log.
type Store interface {
	Append(ctx context.Context, turn Turn) error
	AppendSpeakEntry(ctx context.Context, entry SpeakEntry) error
	AppendCall(ctx context.Context, call Call) error
}

// Speaker is the slice of *persona.Persona the discussion depends on.
type Speaker interface {
	Name() string
	UseModel(model string) error
	Speak(ctx context.Context) (name, content string, usage llm.Usage, output llm.Input, err error)
	Hear(ctx context.Context, name, content string) (memory.Understanding, llm.Usage, error)
	HearDirect(ctx context.Context, name, content string) (memory.Understanding, error)
}

// EndReason says why a discussion stopped.
type EndReason string

const (
	// EndedAllPassed means every speaker passed in the final round.
	EndedAllPassed EndReason = "all-passed"
	// EndedMaxRounds means the round cap was reached first.
	EndedMaxRounds EndReason = "max-rounds"
)

// Result is the outcome of a completed Run.
type Result struct {
	Turns   []Turn
	Entries []SpeakEntry
	Calls   []Call
	Rounds  int
	Ended   EndReason
}

// Discussion holds a round-based discussion among speakers. Each round every
// speaker speaks once in a shuffled order, on a model drawn from the pool.
type Discussion struct {
	topic     string
	speakers  []Speaker
	models    []string
	store     Store
	rng       *rand.Rand
	maxRounds int
	logger    *slog.Logger
	// current is each speaker's model in use, updated whenever UseModel is
	// called, so a heard turn can be attributed to the listener's model.
	current map[string]string
}

// New builds a Discussion. topic is the moderator opening every speaker hears
// before the first round. models is the pool each speaker draws from each
// round. store records the shared transcript.
func New(topic string, speakers []Speaker, models []string, store Store, opts ...Option) (*Discussion, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return nil, errors.New("discussion: topic is required")
	}
	if len(speakers) == 0 {
		return nil, errors.New("discussion: at least one speaker is required")
	}
	seen := make(map[string]bool, len(speakers))
	for _, s := range speakers {
		if s == nil {
			return nil, errors.New("discussion: speaker is nil")
		}
		name := strings.TrimSpace(s.Name())
		if name == "" {
			return nil, errors.New("discussion: speaker name is required")
		}
		if name == moderator {
			return nil, fmt.Errorf("discussion: %q is reserved", moderator)
		}
		if seen[name] {
			return nil, fmt.Errorf("discussion: duplicate speaker %q", name)
		}
		seen[name] = true
	}
	if len(models) == 0 {
		return nil, errors.New("discussion: at least one model is required")
	}
	pool := make([]string, 0, len(models))
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" {
			return nil, errors.New("discussion: model is blank")
		}
		pool = append(pool, m)
	}
	if store == nil {
		return nil, errors.New("discussion: store is required")
	}

	d := &Discussion{
		topic:     topic,
		speakers:  speakers,
		models:    pool,
		store:     store,
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
		maxRounds: defaultMaxRounds,
		logger:    logging.Discard(),
		current:   make(map[string]string, len(speakers)),
	}
	for _, opt := range opts {
		opt(d)
	}
	return d, nil
}

// Run holds the discussion. It opens with every speaker hearing the topic, then
// runs rounds until every speaker passes in one round or maxRounds is reached.
// A speaker or store error aborts the run and returns the turns recorded so far.
func (d *Discussion) Run(ctx context.Context) (Result, error) {
	result := Result{}

	logger := d.logger
	logger.Info("discussion started", "topic", d.topic, "speakers", len(d.speakers), "max_rounds", d.maxRounds)

	if err := d.open(ctx, &result); err != nil {
		return result, err
	}

	last := "" // speaker who closed the previous round; cannot open the next
	for round := 1; round <= d.maxRounds; round++ {
		order := d.order(last)
		models := d.assignModels()
		logger.Info("round started", "round", round, "order", orderNames(order))

		passes := 0
		for i, sp := range order {
			name := sp.Name()
			start := time.Now()
			if err := sp.UseModel(models[name]); err != nil {
				return result, fmt.Errorf("discussion: set model for %s: %w", name, err)
			}
			d.current[name] = models[name]
			speaker, content, usage, _, err := sp.Speak(ctx)
			if err != nil {
				return result, fmt.Errorf("discussion: %s speaks: %w", name, err)
			}
			passed := agent.IsPass(content)
			turn := Turn{
				Round:   round,
				Order:   i + 1,
				Speaker: speaker,
				Model:   models[name],
				Content: content,
				Passed:  passed,
			}
			if err := d.store.Append(ctx, turn); err != nil {
				return result, err
			}
			result.Turns = append(result.Turns, turn)

			entries := speakEntries(round, speaker, models[name], content, passed)
			for _, entry := range entries {
				if err := d.store.AppendSpeakEntry(ctx, entry); err != nil {
					return result, err
				}
				result.Entries = append(result.Entries, entry)
			}
			if err := d.recordCall(ctx, &result, Call{
				Round:   round,
				Speaker: speaker,
				Model:   models[name],
				Purpose: store.CallSpeak,
				Usage:   usage,
			}); err != nil {
				return result, err
			}
			logger.Info("turn", "round", round, "order", i+1, "speaker", speaker, "model", models[name], "passed", passed, "chars", len(content), "outputs", len(entries), "total_tokens", usage.TotalTokens, "cost", usage.Cost, "duration", time.Since(start))

			if passed {
				passes++
				continue
			}
			if err := d.hear(ctx, logger, round, &result, order, speaker, content); err != nil {
				return result, err
			}
		}

		result.Rounds = round
		last = order[len(order)-1].Name()
		if passes == len(order) {
			result.Ended = EndedAllPassed
			logger.Info("discussion ended", "rounds", result.Rounds, "ended", result.Ended, "turns", len(result.Turns))
			return result, nil
		}
	}

	result.Ended = EndedMaxRounds
	logger.Info("discussion ended", "rounds", result.Rounds, "ended", result.Ended, "turns", len(result.Turns))
	return result, nil
}

// orderNames returns the speaker names in speaking order, for logging.
func orderNames(order []Speaker) string {
	names := make([]string, len(order))
	for i, sp := range order {
		names[i] = sp.Name()
	}
	return strings.Join(names, ",")
}

// speakEntries renders a speaking turn into its single recorded entry: the
// answer, or a pass when the persona had nothing to add. A persona's reasoning
// and tool traffic stay private to its own memory and never enter the shared
// discussion.
func speakEntries(round int, speaker, model, content string, passed bool) []SpeakEntry {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	kind := kindMessage
	if passed {
		kind = kindPass
	}
	return []SpeakEntry{{
		Round:     round,
		Speaker:   speaker,
		Model:     model,
		Kind:      kind,
		Content:   content,
		CreatedAt: time.Now(),
	}}
}

// open records the moderator opening and has every speaker hear it.
func (d *Discussion) open(ctx context.Context, result *Result) error {
	turn := Turn{Speaker: moderator, Content: d.topic}
	if err := d.store.Append(ctx, turn); err != nil {
		return err
	}
	result.Turns = append(result.Turns, turn)

	entry := SpeakEntry{Speaker: moderator, Kind: kindOpening, Content: d.topic, CreatedAt: time.Now()}
	if err := d.store.AppendSpeakEntry(ctx, entry); err != nil {
		return err
	}
	result.Entries = append(result.Entries, entry)

	for _, sp := range d.speakers {
		model := d.pickModel()
		if err := sp.UseModel(model); err != nil {
			return fmt.Errorf("discussion: set model for %s: %w", sp.Name(), err)
		}
		d.current[sp.Name()] = model
		if _, err := sp.HearDirect(ctx, moderator, d.topic); err != nil {
			return fmt.Errorf("discussion: %s hears the opening: %w", sp.Name(), err)
		}
	}
	d.logger.Debug("opening heard", "speakers", len(d.speakers))
	return nil
}

// hear delivers a spoken turn to every other speaker, recording each listener's
// comprehension call and its cost.
func (d *Discussion) hear(ctx context.Context, logger *slog.Logger, round int, result *Result, order []Speaker, speaker, content string) error {
	for _, other := range order {
		if other.Name() == speaker {
			continue
		}
		start := time.Now()
		_, usage, err := other.Hear(ctx, speaker, content)
		if err != nil {
			return fmt.Errorf("discussion: %s hears %s: %w", other.Name(), speaker, err)
		}
		if err := d.recordCall(ctx, result, Call{
			Round:   round,
			Speaker: other.Name(),
			Model:   d.current[other.Name()],
			Purpose: store.CallUnderstand,
			Usage:   usage,
		}); err != nil {
			return err
		}
		logger.Debug("hears", "speaker", speaker, "listener", other.Name(), "chars", len(content), "total_tokens", usage.TotalTokens, "cost", usage.Cost, "duration", time.Since(start))
	}
	return nil
}

// recordCall appends a model call to the store and the result.
func (d *Discussion) recordCall(ctx context.Context, result *Result, call Call) error {
	if err := d.store.AppendCall(ctx, call); err != nil {
		return err
	}
	result.Calls = append(result.Calls, call)
	return nil
}

// order shuffles the roster. The previous round's closer cannot open the new
// round; swapping the first two positions guarantees that without a retry loop.
func (d *Discussion) order(last string) []Speaker {
	order := make([]Speaker, len(d.speakers))
	copy(order, d.speakers)
	d.rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	if last != "" && len(order) > 1 && order[0].Name() == last {
		order[0], order[1] = order[1], order[0]
	}
	return order
}

// assignModels draws one model per speaker for a round, with replacement.
func (d *Discussion) assignModels() map[string]string {
	models := make(map[string]string, len(d.speakers))
	for _, sp := range d.speakers {
		models[sp.Name()] = d.pickModel()
	}
	return models
}

func (d *Discussion) pickModel() string {
	return d.models[d.rng.Intn(len(d.models))]
}
