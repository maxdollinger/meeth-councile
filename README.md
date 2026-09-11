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

Each agent is just a system prompt. No other differentiation.

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

## Context management

Each agent's context per turn is built as:

```
[system: persona + PASS instructions]
[user: moderator's opening prompt]              <- always present, pinned
[user: "Summary of earlier discussion: ..."]    <- once one exists
[last 4 rounds, verbatim, relabeled so the speaking
 agent's own prior turns look like `assistant`
 and everyone else's look like `user`]
```

**Rolling summary.** Once the discussion exceeds 4 rounds, a summarizer call
runs after each round: it's given the current summary plus the **last 5**
completed rounds (raw, unrelabeled, speaker-tagged text) and produces an
updated summary. The 4-round overlap between what's re-summarized and what's
still shown verbatim keeps the summary grounded in source text rather than
compounding through repeated summary-of-summary passes.

## Tooling: research assistant

Debate agents don't call search/fetch tools directly. They have access to a
single tool, `research_assistant(query)`, which is itself a small nested
agent: it runs its own tool loop (websearch + fetch) internally, capped at a
few turns, and returns only a synthesized answer to the calling agent. This
keeps raw search traffic out of the debate transcript entirely — search
results are private to whichever agent requested them, and only their
conclusions (in their own words) ever reach the shared discussion. Queries
sent to the research assistant are self-contained (no debate history passed
in), keeping it a clean, independently testable component.

## Logging

Two separate records are kept:

- **Model-facing transcript** — only real (non-PASS) contributions, used to
  build future context. This is what the agents see.
- **Full run log** — everything: every reply including PASS turns, and every
  research assistant call, kept for review/debugging but never fed back into
  any model.

## Explicitly out of scope

Streaming, an agent framework, memory/RAG beyond the transcript, multiple
tools, a dynamic/LLM-driven moderator. None of it is needed for this shape
of project.

## Build order

1. LiteLLM + tool clients — bare "send messages, get completion" plumbing.
2. Single-turn function for one agent: tool loop resolved, PASS detected.
3. Round loop: shuffle, no-repeat boundary check, PASS/end-condition logic
   (no summarization yet — fine for short test runs).
4. Context builder: moderator pin + last-4-rounds windowing.
5. Research assistant as a nested agent, wired in as the debate agents' tool.
6. Rolling summarizer, wired in once rounds exceed 4.
7. Logging/output (write the full run to disk as it progresses).
