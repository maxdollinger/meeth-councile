# Metaethics Debate — Multi-Agent LLM Discussion

Five LLM agents, each arguing from a distinct metaethical position, hold a
multi-round discussion on a given topic. No agent framework — this is plain
orchestration logic around chat-completion calls, since the requirements
(single API schema via OpenRouter, one tool, no streaming) don't need anything
heavier.

## Agents

Five fixed personas, one per dominant metaethical position:

- **Moral realism** — moral facts exist and are mind-independent
- **Error theory** — moral claims aim at objective truth but are all false
- **Expressivism / non-cognitivism** — moral claims express attitudes, aren't truth-apt
- **Constructivism** — moral facts arise from an idealized rational/social procedure
- **Relativism** — moral truths hold relative to culture or individual

Each agent's system prompt is the shared prompt (`internal/prompts/common.md`)
plus a short position-specific prompt (`internal/prompts/personas/*.md`). Each
also has a private memory (see below); nothing else differentiates them.

## Model access

All agents (and the research assistant, see below) call out through **LiteLLM**,
so the rest of the system only ever deals with one API schema regardless of
which underlying model backs each role. The research assistant can run on a
cheaper/faster model than the five debate personas, since synthesis is a
lighter task than holding a philosophical position.

## Discussion mechanics

- **Moderator opening.** A single pinned message states the discussion topic.
  It's always present in every agent's context and is never summarized away.
- **Turn order.** Each round, the 5 agents are shuffled into a random speaking
  order. If the resulting first speaker is the same agent who spoke last
  (the last agent who actually produced content, not just the last in order)
  in the previous round, the order is reshuffled.
- **PASS.** An agent may reply with the exact string `PASS` if it has nothing
  to add. A PASS turn is logged for the full run record but is **never**
  added to the shared transcript that other agents see — as far as the
  discussion is concerned, it didn't happen.
- **End condition.** If every agent passes in the same round, the discussion
  ends.

## Memory

Each persona has a private, SQLite-backed memory (`internal/memory`). Only the
persona reads and writes it; there is no shared transcript. It is pure
persistence and prompt assembly — it never calls a model itself.

- **System prompt.** The common prompt and the persona's own prompt, assembled
  into one system message. Both are snapshotted when the persona's memory is
  first created, so later edits to the files don't rewrite history.
- **Hear.** When a persona hears another turn, the raw text is first run through
  a comprehension call that produces a personalized interpretation. Only that
  interpretation is stored, never the raw words. The persona reasons from what
  it made of what was said, not from what was actually said.
- **Speak.** A speaking turn is stored whole: reasoning, tool calls, tool
  outputs, and the final message. Future turns replay that sequence verbatim, so
  a persona both sees its own past answers and resumes its own chain of thought.
- **Moderator opening.** Just the first thing a persona hears, personalized like
  anything else. It is always replayed verbatim and is never summarized away.

Because memory is per-persona, two agents can hold incompatible readings of the
same exchange, which is the point.

### Compaction

Memory is bounded: each persona is tuned for an assumed 32k context window
(`internal/memory/compact.go`). When the replayed history passes the budget, the
persona's oldest entries are folded into a single summary, produced by one
model call through the same path as any other understanding. From then on the
history is the opening, the summary, and the entries after it; the raw covered
entries stay in the database for audit but are no longer replayed. A later
compaction folds the previous summary into the new one, so there is always at
most one. The moderator opening is deliberately excluded from the summary and
always replayed first.

## Tooling: research assistant

Debate agents don't call search/fetch tools directly. They have access to a
single tool, `research_assistant(query)`, which is itself a small nested
agent: it runs its own tool loop (websearch + fetch) internally, capped at a
few turns, and returns only a synthesized answer to the calling agent. This
keeps raw search traffic out of the debate entirely — search results are
private to whichever agent requested them.

A research result is not a special case in memory. It is heard content, so it
goes through the same comprehension step as anything else: the persona stores
only its own interpretation, and the raw answer never enters its context. The
query, caller, and raw output are written to a separate research log table for
audit, and are never fed back into any model.

Queries sent to the research assistant are self-contained (no debate history
passed in), keeping it a clean, independently testable component.

## Logging

Several separate SQLite records are kept:

- **Per-persona memory** — each persona's own answers plus its personalized
  understandings of everything it heard. This is what that persona reasons
  from, and only that persona reads it.
- **Discussion log** — every output a speaking turn produced, in order:
  reasoning, tool calls, tool outputs, and the final message. The web
  transcript renders these entries by time, rather than only the completed
  turns.
- **Model-call cost log** — every debate model call (a persona speaking, or
  comprehending what it heard) with its model, round, purpose, token usage, and
  cost. Together with the research log below, this covers every paid call; the
  per-turn cost total is `SUM(cost)` across both tables.
- **Research log** — every research-assistant call: query, caller, and raw
  output, kept for review/debugging but never fed back into any model.

Alongside the SQLite records, the process emits structured logs (via
`log/slog`) to stderr so a running server can be watched live: store and
server startup, discussion/round/turn progress, every model call with tokens
and cost, and research calls. Tune them with:

- `LOG_LEVEL` — `debug`, `info` (default), `warn`, or `error`. `debug` adds
  per-step agent detail, comprehension calls, and HTTP request lines.
- `LOG_FORMAT` — `text` (default, logfmt-style) or `json`.

The web server binds `ADDR` (default `:8080`).

## Explicitly out of scope

Streaming, an agent framework, RAG/embedding retrieval, tools beyond the
research assistant, a dynamic/LLM-driven moderator. None of it is needed for
this shape of project.

## Build order

1. LiteLLM + tool clients — bare "send messages, get completion" plumbing. Done.
2. Single-turn agent: tool loop resolved, PASS detected. Done.
3. Per-persona memory: SQLite persistence, system-prompt assembly, stored
   answers and personalized understandings. Done.
4. Persona: `Speak`/`Hear`, wiring the comprehension call and the research tool.
5. Round loop: shuffle, no-repeat boundary check, PASS/end-condition logic.
6. Research assistant as a nested agent plus its audit log.
7. Output/logging.
