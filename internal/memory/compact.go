package memory

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/maxdollinger/meeth-councile/internal/llm"
	"github.com/maxdollinger/meeth-councile/internal/store"
)

// ContextWindowTokens is the assumed model context window this budget is tuned
// for. All personas run on the same figure.
const ContextWindowTokens = 32000

const (
	// historyBudgetTokens caps the replayed transcript before an older part of
	// it is compacted. The rest of the window holds the system prompt, tool
	// schema, the turn instruction, and the model's output.
	historyBudgetTokens = 12000
	// keepRecentTokens is roughly how much of the newest history stays verbatim;
	// everything older is folded into a summary.
	keepRecentTokens = 4000
	// charsPerToken over-estimates token count for German text on purpose.
	charsPerToken = 3.2
	// msgOverhead approximates the per-message chat wrapper.
	msgOverhead = 4
	// moderatorSpeaker labels the opening turn, which is never summarized.
	moderatorSpeaker = "moderator"
)

// summaryInstruction closes the compaction prompt.
const summaryInstruction = "Fasse den folgenden, älteren Teil des Gesprächs in deinen eigenen Worten komprimiert zusammen. Diese Zusammenfassung ersetzt das Original in deiner Erinnerung: Halte fest, was du für die weitere Diskussion brauchst — die vertretenen Positionen, die vorgebrachten Argumente und Einwände und deine eigene Haltung dazu. Die Ausgangsfrage gehört nicht dazu; sie bleibt dir ohnehin wörtlich erhalten. Erfinde nichts hinzu. Antworte nur mit der Zusammenfassung."

// view is a persona's effective memory: the opening, at most one summary, and
// the entries after the summary's coverage.
type view struct {
	opening  *Entry
	summary  *Entry
	coverage int
	recent   []Entry
}

// buildView resolves a memory's entries into the view that History replays. The
// opening is always kept verbatim; only the newest summary counts, and it
// supersedes every entry up to its coverage.
func buildView(entries []Entry) view {
	var v view
	for i := range entries {
		e := &entries[i]
		if e.Kind == KindSummary {
			if v.summary == nil || e.Seq > v.summary.Seq {
				v.summary = e
				v.coverage = coverageOf(*e)
			}
			continue
		}
		if isOpening(*e) && v.opening == nil {
			v.opening = e
		}
	}
	for _, e := range entries {
		if isOpening(e) || e.Kind == KindSummary || e.Seq <= v.coverage {
			continue
		}
		v.recent = append(v.recent, e)
	}
	return v
}

// coverageOf reads the last sequence number a summary replaces. Older memories
// wrote no marker, in which case everything before the summary row is covered.
func coverageOf(e Entry) int {
	if n, err := strconv.Atoi(strings.TrimSpace(e.Source)); err == nil && n >= 0 {
		return n
	}
	return e.Seq - 1
}

// isOpening reports whether e is the moderator's opening question.
func isOpening(e Entry) bool {
	return e.Kind == KindUnderstanding && strings.EqualFold(strings.TrimSpace(e.Speaker), moderatorSpeaker)
}

// ShouldCompact reports whether the replayed history exceeds the budget.
func (m *Memory) ShouldCompact(ctx context.Context) (bool, error) {
	entries, err := m.Entries(ctx)
	if err != nil {
		return false, err
	}
	return estimateView(buildView(entries)) > historyBudgetTokens, nil
}

// SummaryPrompt builds the compaction model call and returns the sequence
// number the summary will cover. ok is false when there is nothing new to
// summarize. The original question is deliberately left out of the source: it
// is replayed verbatim, never summarized.
func (m *Memory) SummaryPrompt(ctx context.Context) (prompt llm.Input, coversThrough int, ok bool, err error) {
	entries, err := m.Entries(ctx)
	if err != nil {
		return nil, 0, false, err
	}
	v := buildView(entries)
	if len(v.recent) == 0 {
		return nil, 0, false, nil
	}

	remaining := keepRecentTokens
	keepFrom := 0
	for i := len(v.recent) - 1; i >= 0; i-- {
		size := estimateText(v.recent[i].Content) + msgOverhead
		if size > remaining {
			break
		}
		remaining -= size
		keepFrom = v.recent[i].Seq
	}

	coverage := v.coverage
	if keepFrom == 0 {
		// Not even the newest entry fits the verbatim window: fold everything.
		coverage = v.recent[len(v.recent)-1].Seq
	} else {
		coverage = keepFrom - 1
	}
	if coverage <= v.coverage {
		return nil, 0, false, nil
	}

	var b strings.Builder
	if v.summary != nil {
		b.WriteString("Bisherige Zusammenfassung:\n")
		b.WriteString(v.summary.Content)
		b.WriteString("\n\n")
	}
	for _, e := range entries {
		if isOpening(e) || e.Kind == KindSummary {
			continue
		}
		if e.Seq > v.coverage && e.Seq <= coverage {
			b.WriteString(label(e.Speaker, e.Content))
			b.WriteString("\n\n")
		}
	}
	source := strings.TrimSpace(b.String())
	if source == "" {
		return nil, 0, false, nil
	}

	return llm.Input{
		llm.System(m.SystemPrompt()),
		llm.User(source),
		llm.User(summaryInstruction),
	}, coverage, true, nil
}

// AppendSummary stores a compaction summary that replaces every entry up to and
// including coversThrough. The opening is outside that range and stays put.
func (m *Memory) AppendSummary(ctx context.Context, content string, coversThrough int) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return errors.New("memory: summary has no content")
	}
	if err := m.repo.Append(ctx, m.id, store.Entry{
		Kind:    KindSummary,
		Speaker: m.persona,
		Content: content,
		Items:   llm.Input{llm.Assistant(content)},
		Source:  strconv.Itoa(coversThrough),
	}); err != nil {
		return err
	}
	m.logger.Info("memory summary appended", "persona", m.persona, "covers_through", coversThrough, "chars", len(content))
	return nil
}

// estimateView approximates the token count of the replayed history.
func estimateView(v view) int {
	total := 0
	if v.opening != nil {
		total += estimateText(label(v.opening.Speaker, v.opening.Content)) + msgOverhead
	}
	if v.summary != nil {
		total += estimateText(v.summary.Content) + msgOverhead
	}
	for _, e := range v.recent {
		total += estimateText(e.Content) + msgOverhead
	}
	return total
}

// estimateText converts text length to an approximate token count.
func estimateText(s string) int {
	if s == "" {
		return 0
	}
	return int(math.Ceil(float64(len(s)) / charsPerToken))
}
