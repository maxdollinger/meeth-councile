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
// cost log. The read methods let a restarted run resume from what is already
// recorded instead of starting over.
type Store interface {
	Append(ctx context.Context, turn Turn) error
	AppendSpeakEntry(ctx context.Context, entry SpeakEntry) error
	AppendCall(ctx context.Context, call Call) error
	Entries(ctx context.Context) ([]Turn, error)
	SpeakEntries(ctx context.Context) ([]SpeakEntry, error)
	Calls(ctx context.Context) ([]Call, error)
}

// Speaker is the slice of *persona.Persona the discussion depends on.
type Speaker interface {
	Name() string
	UseModel(model string) error
	Speak(ctx context.Context) (name, content string, usage llm.Usage, output llm.Input, err error)
	Hear(ctx context.Context, name, content string) (memory.Understanding, llm.Usage, error)
	HearDirect(ctx context.Context, name, content string) (memory.Understanding, error)
	// HasHeard reports whether this speaker already holds an understanding of a
	// heard turn, so a resumed run does not deliver it twice.
	HasHeard(ctx context.Context, name, content string) (bool, error)
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
	// roundDelay stretches the discussion by pausing between rounds.
	roundDelay time.Duration
	logger     *slog.Logger
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

// Run holds the discussion. When the store already has a transcript it resumes
// from there; otherwise it opens with every speaker hearing the topic. Rounds
// continue until every speaker passes in one round or maxRounds is reached. A
// speaker or store error aborts the run and returns the turns recorded so far.
func (d *Discussion) Run(ctx context.Context) (Result, error) {
	logger := d.logger
	logger.Info("discussion started", "topic", d.topic, "speakers", len(d.speakers), "max_rounds", d.maxRounds, "round_delay", d.roundDelay)

	existing, err := d.store.Entries(ctx)
	if err != nil {
		return Result{}, err
	}
	if len(existing) == 0 {
		return d.runFresh(ctx)
	}
	return d.runResume(ctx, existing)
}

// runFresh opens a new discussion and runs it from the first round.
func (d *Discussion) runFresh(ctx context.Context) (Result, error) {
	result := Result{}
	logger := d.logger

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
			turn, err := d.speak(ctx, logger, &result, round, i+1, sp, models[sp.Name()])
			if err != nil {
				return result, err
			}
			if turn.Passed {
				passes++
				continue
			}
			if err := d.hear(ctx, logger, round, &result, order, turn.Speaker, turn.Content); err != nil {
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
		if round < d.maxRounds {
			if err := d.pause(ctx); err != nil {
				return result, err
			}
		}
	}

	result.Ended = EndedMaxRounds
	logger.Info("discussion ended", "rounds", result.Rounds, "ended", result.Ended, "turns", len(result.Turns))
	return result, nil
}

// runResume picks a stored discussion back up. It replays the parts that were
// recorded but not completed (pending comprehension calls), finishes the round
// that was interrupted, then runs further rounds up to the cap. Every persona's
// own memory already holds what it heard and said, so nothing is re-spoken.
func (d *Discussion) runResume(ctx context.Context, existing []Turn) (Result, error) {
	result := Result{Turns: existing}
	logger := d.logger

	entries, err := d.store.SpeakEntries(ctx)
	if err != nil {
		return result, err
	}
	result.Entries = entries
	calls, err := d.store.Calls(ctx)
	if err != nil {
		return result, err
	}
	result.Calls = calls
	logger.Info("resuming discussion", "turns", len(existing), "entries", len(entries), "calls", len(calls))

	// Give every speaker a model now so comprehension calls made while catching
	// up can be attributed to a model.
	for _, sp := range d.speakers {
		model := d.pickModel()
		if err := sp.UseModel(model); err != nil {
			return result, fmt.Errorf("discussion: set model for %s: %w", sp.Name(), err)
		}
		d.current[sp.Name()] = model
	}

	// Re-deliver the opening only to a speaker that does not somehow have it.
	if _, opened := moderatorTurn(existing); opened {
		for _, sp := range d.speakers {
			heard, err := sp.HasHeard(ctx, moderator, d.topic)
			if err != nil {
				return result, fmt.Errorf("discussion: check opening for %s: %w", sp.Name(), err)
			}
			if heard {
				continue
			}
			if _, err := sp.HearDirect(ctx, moderator, d.topic); err != nil {
				return result, fmt.Errorf("discussion: %s hears the opening: %w", sp.Name(), err)
			}
		}
	}

	// A recorded turn whose comprehension calls never finished is caught up here.
	if err := d.deliverPendingHears(ctx, logger, &result, existing); err != nil {
		return result, err
	}

	rounds := groupDebateRounds(existing)
	lastRound := highestRound(rounds)

	// The discussion may already be over.
	if lastRound > 0 && len(rounds[lastRound]) == len(d.speakers) {
		result.Rounds = lastRound
		if allTurnPassed(rounds[lastRound]) {
			result.Ended = EndedAllPassed
			logger.Info("discussion ended", "rounds", result.Rounds, "ended", result.Ended, "turns", len(result.Turns))
			return result, nil
		}
		if lastRound >= d.maxRounds {
			result.Ended = EndedMaxRounds
			logger.Info("discussion ended", "rounds", result.Rounds, "ended", result.Ended, "turns", len(result.Turns))
			return result, nil
		}
	}

	// Finish the interrupted round, if there is one.
	if lastRound > 0 && len(rounds[lastRound]) < len(d.speakers) {
		if err := d.finishRound(ctx, logger, &result, lastRound, rounds); err != nil {
			return result, err
		}
		result.Rounds = lastRound
		if allTurnPassed(rounds[lastRound]) {
			result.Ended = EndedAllPassed
			logger.Info("discussion ended", "rounds", result.Rounds, "ended", result.Ended, "turns", len(result.Turns))
			return result, nil
		}
	}

	// Continue with full rounds after the last one.
	last := ""
	if lastRound > 0 {
		last = closer(rounds[lastRound])
		if lastRound < d.maxRounds {
			if err := d.pause(ctx); err != nil {
				return result, err
			}
		}
	}
	for round := lastRound + 1; round <= d.maxRounds; round++ {
		order := d.order(last)
		models := d.assignModels()
		logger.Info("round started", "round", round, "order", orderNames(order))

		passes := 0
		for i, sp := range order {
			turn, err := d.speak(ctx, logger, &result, round, i+1, sp, models[sp.Name()])
			if err != nil {
				return result, err
			}
			if turn.Passed {
				passes++
				continue
			}
			if err := d.hear(ctx, logger, round, &result, order, turn.Speaker, turn.Content); err != nil {
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
		if round < d.maxRounds {
			if err := d.pause(ctx); err != nil {
				return result, err
			}
		}
	}

	result.Ended = EndedMaxRounds
	logger.Info("discussion ended", "rounds", result.Rounds, "ended", result.Ended, "turns", len(result.Turns))
	return result, nil
}

// speak runs one speaking turn: assign the model, get the answer, and record the
// turn, its speak entry, and its model call.
func (d *Discussion) speak(ctx context.Context, logger *slog.Logger, result *Result, round, order int, sp Speaker, model string) (Turn, error) {
	name := sp.Name()
	start := time.Now()
	if err := sp.UseModel(model); err != nil {
		return Turn{}, fmt.Errorf("discussion: set model for %s: %w", name, err)
	}
	d.current[name] = model
	speaker, content, usage, _, err := sp.Speak(ctx)
	if err != nil {
		return Turn{}, fmt.Errorf("discussion: %s speaks: %w", name, err)
	}
	passed := agent.IsPass(content)
	turn := Turn{
		Round:   round,
		Order:   order,
		Speaker: speaker,
		Model:   model,
		Content: content,
		Passed:  passed,
	}
	if err := d.store.Append(ctx, turn); err != nil {
		return Turn{}, err
	}
	result.Turns = append(result.Turns, turn)

	entries := speakEntries(round, speaker, model, content, passed)
	for _, entry := range entries {
		if err := d.store.AppendSpeakEntry(ctx, entry); err != nil {
			return Turn{}, err
		}
		result.Entries = append(result.Entries, entry)
	}
	if err := d.recordCall(ctx, result, Call{
		Round:   round,
		Speaker: speaker,
		Model:   model,
		Purpose: store.CallSpeak,
		Usage:   usage,
	}); err != nil {
		return Turn{}, err
	}
	logger.Info("turn", "round", round, "order", order, "speaker", speaker, "model", model, "passed", passed, "chars", len(content), "outputs", len(entries), "total_tokens", usage.TotalTokens, "cost", usage.Cost, "duration", time.Since(start))
	return turn, nil
}

// finishRound has the speakers who have not yet spoken in round produce their
// turns, then delivers each new turn to the others.
func (d *Discussion) finishRound(ctx context.Context, logger *slog.Logger, result *Result, round int, rounds map[int][]Turn) error {
	done := make(map[string]bool, len(d.speakers))
	nextOrder := 0
	for _, turn := range rounds[round] {
		done[turn.Speaker] = true
		if turn.Order > nextOrder {
			nextOrder = turn.Order
		}
	}
	for _, sp := range d.order("") {
		if done[sp.Name()] {
			continue
		}
		nextOrder++
		turn, err := d.speak(ctx, logger, result, round, nextOrder, sp, d.pickModel())
		if err != nil {
			return err
		}
		rounds[round] = append(rounds[round], turn)
		if turn.Passed {
			continue
		}
		if err := d.hear(ctx, logger, round, result, d.speakers, turn.Speaker, turn.Content); err != nil {
			return err
		}
	}
	return nil
}

// deliverPendingHears catches up comprehension calls for recorded, non-passed
// turns a listener has not yet heard.
func (d *Discussion) deliverPendingHears(ctx context.Context, logger *slog.Logger, result *Result, turns []Turn) error {
	for _, turn := range turns {
		if turn.Round == 0 || turn.Passed || strings.TrimSpace(turn.Content) == "" {
			continue
		}
		for _, other := range d.speakers {
			if other.Name() == turn.Speaker {
				continue
			}
			heard, err := other.HasHeard(ctx, turn.Speaker, turn.Content)
			if err != nil {
				return fmt.Errorf("discussion: check %s heard %s: %w", other.Name(), turn.Speaker, err)
			}
			if heard {
				continue
			}
			start := time.Now()
			_, usage, err := other.Hear(ctx, turn.Speaker, turn.Content)
			if err != nil {
				return fmt.Errorf("discussion: %s hears %s: %w", other.Name(), turn.Speaker, err)
			}
			if err := d.recordCall(ctx, result, Call{
				Round:   turn.Round,
				Speaker: other.Name(),
				Model:   d.current[other.Name()],
				Purpose: store.CallUnderstand,
				Usage:   usage,
			}); err != nil {
				return err
			}
			logger.Debug("hears", "speaker", turn.Speaker, "listener", other.Name(), "chars", len(turn.Content), "total_tokens", usage.TotalTokens, "cost", usage.Cost, "duration", time.Since(start))
		}
	}
	return nil
}

// groupDebateRounds buckets the debate turns (round >= 1) by round.
func groupDebateRounds(turns []Turn) map[int][]Turn {
	out := make(map[int][]Turn)
	for _, turn := range turns {
		if turn.Round >= 1 {
			out[turn.Round] = append(out[turn.Round], turn)
		}
	}
	return out
}

// highestRound returns the greatest round present, or 0 when there is none.
func highestRound(rounds map[int][]Turn) int {
	highest := 0
	for round := range rounds {
		if round > highest {
			highest = round
		}
	}
	return highest
}

// allTurnPassed reports whether a full round of turns were all passes.
func allTurnPassed(turns []Turn) bool {
	for _, turn := range turns {
		if !turn.Passed {
			return false
		}
	}
	return len(turns) > 0
}

// closer returns the speaker of the last turn in the round.
func closer(turns []Turn) string {
	last := ""
	highest := -1
	for _, turn := range turns {
		if turn.Order > highest {
			highest = turn.Order
			last = turn.Speaker
		}
	}
	return last
}

// moderatorTurn returns the recorded moderator opening, if any.
func moderatorTurn(turns []Turn) (Turn, bool) {
	for _, turn := range turns {
		if turn.Round == 0 && turn.Speaker == moderator {
			return turn, true
		}
	}
	return Turn{}, false
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

// pause stretches the discussion by waiting between rounds. It returns early if
// the context is cancelled, so shutdown is not held up by a pending delay.
func (d *Discussion) pause(ctx context.Context) error {
	if d.roundDelay <= 0 {
		return nil
	}
	d.logger.Info("round pause", "delay", d.roundDelay)
	timer := time.NewTimer(d.roundDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
