package memory

import (
	"context"
	"strings"

	"github.com/maxdollinger/meeth-councile/internal/llm"
)

// comprehensionInstruction closes the comprehension prompt. It asks the model
// to record the persona's own interpretation while keeping what was actually
// said or reported separate from what the persona makes of it.
const comprehensionInstruction = "This is something you have heard or found. In your own words, record what you now understand from it. Separate what was actually said or reported from your own interpretation. Reply with only that understanding."

// SystemPrompt is the persona's assembled system prompt: the shared prompt plus
// the position-specific one. Pass it to agent.New; it is not part of History.
func (m *Memory) SystemPrompt() string {
	return strings.TrimSpace(m.commonPrompt + "\n\n" + m.personaPrompt)
}

// History returns the persona's stored turns in order, ready to pass to
// agent.Run. It contains no system message; the caller adds SystemPrompt.
func (m *Memory) History(ctx context.Context) (llm.Input, error) {
	entries, err := m.Entries(ctx)
	if err != nil {
		return nil, err
	}
	var in llm.Input
	for _, e := range entries {
		in = append(in, e.Items...)
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
