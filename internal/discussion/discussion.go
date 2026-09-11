// Package discussion runs the round-based debate: each round every persona
// speaks once in a freshly shuffled order on a randomly assigned model, hears
// the others, and the discussion ends when a whole round passes.
package discussion

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/maxdollinger/meeth-councile/internal/agent"
	"github.com/maxdollinger/meeth-councile/internal/memory"
	"github.com/maxdollinger/meeth-councile/internal/store"
)

// moderator is the speaker name of the opening topic turn.
const moderator = "moderator"

const defaultMaxRounds = 20

// Turn is one recorded discussion turn.
type Turn = store.Turn

// Store persists the shared transcript.
type Store interface {
	Append(ctx context.Context, discussionID string, turn Turn) error
}

// Speaker is the slice of *persona.Persona the discussion depends on.
type Speaker interface {
	Name() string
	UseModel(model string) error
	Speak(ctx context.Context) (string, string, error)
	Hear(ctx context.Context, name, content string) (memory.Understanding, error)
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
	Turns  []Turn
	Rounds int
	Ended  EndReason
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
	}
	for _, opt := range opts {
		opt(d)
	}
	return d, nil
}

// Run holds the discussion. It opens with every speaker hearing the topic, then
// runs rounds until every speaker passes in one round or maxRounds is reached.
// A speaker or store error aborts the run and returns the turns recorded so far.
func (d *Discussion) Run(ctx context.Context, discussionID string) (Result, error) {
	result := Result{}
	if strings.TrimSpace(discussionID) == "" {
		return result, errors.New("discussion: discussion id is required")
	}

	if err := d.open(ctx, discussionID, &result); err != nil {
		return result, err
	}

	last := "" // speaker who closed the previous round; cannot open the next
	for round := 1; round <= d.maxRounds; round++ {
		order := d.order(last)
		models := d.assignModels()

		passes := 0
		for i, sp := range order {
			name := sp.Name()
			if err := sp.UseModel(models[name]); err != nil {
				return result, fmt.Errorf("discussion: set model for %s: %w", name, err)
			}
			speaker, content, err := sp.Speak(ctx)
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
			if err := d.store.Append(ctx, discussionID, turn); err != nil {
				return result, err
			}
			result.Turns = append(result.Turns, turn)

			if passed {
				passes++
				continue
			}
			if err := d.hear(ctx, order, speaker, content); err != nil {
				return result, err
			}
		}

		result.Rounds = round
		last = order[len(order)-1].Name()
		if passes == len(order) {
			result.Ended = EndedAllPassed
			return result, nil
		}
	}

	result.Ended = EndedMaxRounds
	return result, nil
}

// open records the moderator opening and has every speaker hear it.
func (d *Discussion) open(ctx context.Context, discussionID string, result *Result) error {
	turn := Turn{Speaker: moderator, Content: d.topic}
	if err := d.store.Append(ctx, discussionID, turn); err != nil {
		return err
	}
	result.Turns = append(result.Turns, turn)

	for _, sp := range d.speakers {
		if err := sp.UseModel(d.pickModel()); err != nil {
			return fmt.Errorf("discussion: set model for %s: %w", sp.Name(), err)
		}
		if _, err := sp.Hear(ctx, moderator, d.topic); err != nil {
			return fmt.Errorf("discussion: %s hears the opening: %w", sp.Name(), err)
		}
	}
	return nil
}

// hear delivers a spoken turn to every other speaker.
func (d *Discussion) hear(ctx context.Context, order []Speaker, speaker, content string) error {
	for _, other := range order {
		if other.Name() == speaker {
			continue
		}
		if _, err := other.Hear(ctx, speaker, content); err != nil {
			return fmt.Errorf("discussion: %s hears %s: %w", other.Name(), speaker, err)
		}
	}
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
