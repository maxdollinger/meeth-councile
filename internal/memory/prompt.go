package memory

import (
	"context"
	"strings"

	"github.com/maxdollinger/meeth-councile/internal/llm"
)

// comprehensionInstruction closes the comprehension prompt. It asks the model
// to record the persona's own interpretation while keeping what was actually
// said or reported separate from what the persona makes of it.
const comprehensionInstruction = "Das ist etwas, das du gehört oder gefunden hast. Halte in deinen eigenen Worten fest, was du jetzt daraus verstehst. Trenne, was tatsächlich gesagt oder berichtet wurde, von deiner eigenen Deutung. Antworte nur mit diesem Verständnis."

// SystemPrompt is the persona's assembled system prompt: the shared prompt plus
// the position-specific one. Pass it to agent.New; it is not part of History.
func (m *Memory) SystemPrompt() string {
	return strings.TrimSpace(m.commonPrompt + "\n\n" + m.personaPrompt)
}

// History returns the persona's stored turns in order, ready to pass to
// agent.Run. It replays text only: its own past answers as assistant messages
// and what it heard as user messages labeled by speaker. The stored agent loops
// and raw sources are never replayed. History contains no system message; the
// caller adds SystemPrompt.
//
// The moderator opening is always replayed first, verbatim. Once the memory has
// been compacted, the summary replaces every entry it covers, so only the
// opening, the summary, and the entries after the summary's coverage appear.
func (m *Memory) History(ctx context.Context) (llm.Input, error) {
	entries, err := m.Entries(ctx)
	if err != nil {
		return nil, err
	}
	v := buildView(entries)
	var in llm.Input
	if v.opening != nil {
		in = append(in, llm.User(label(v.opening.Speaker, v.opening.Content)))
	}
	if v.summary != nil {
		in = append(in, llm.User("Zusammenfassung des bisherigen Gesprächs:\n"+v.summary.Content))
	}
	for _, e := range v.recent {
		if e.Content == "" {
			continue
		}
		switch e.Kind {
		case KindAnswer:
			in = append(in, llm.Assistant(e.Content))
		case KindUnderstanding:
			in = append(in, llm.User(label(e.Speaker, e.Content)))
		}
	}
	return in, nil
}

// ComprehensionPrompt builds the input for the persona's comprehension call:
// its system prompt, everything it already understands, the current turn (nil
// between speaking turns), and the new answer to interpret. The caller runs the
// model and hands the reply to AppendUnderstanding.
func (m *Memory) ComprehensionPrompt(ctx context.Context, a Answer, turn llm.Input) (llm.Input, error) {
	history, err := m.History(ctx)
	if err != nil {
		return nil, err
	}
	in := make(llm.Input, 0, len(history)+len(turn)+3)
	in = append(in, llm.System(m.SystemPrompt()))
	in = append(in, history...)
	in = append(in, turn...)
	in = append(in, llm.User(label(a.Name, a.Content)))
	in = append(in, llm.User(comprehensionInstruction))
	return in, nil
}
