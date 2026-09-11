-- Persona memories: one row per persona. There is a single discussion, so the
-- persona name alone identifies a memory.
CREATE TABLE IF NOT EXISTS memories (
    persona        TEXT PRIMARY KEY,
    common_prompt  TEXT NOT NULL,
    persona_prompt TEXT NOT NULL,
    created_at     INTEGER NOT NULL
);

-- Memory entries: a persona's own turns and its understandings of what it heard.
-- content is the persona's own text (its answer or its understanding summary);
-- items is the full agent loop, kept for audit but never replayed into history.
CREATE TABLE IF NOT EXISTS entries (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    memory_id  TEXT NOT NULL REFERENCES memories(persona) ON DELETE CASCADE,
    seq        INTEGER NOT NULL,
    kind       TEXT NOT NULL,
    speaker    TEXT NOT NULL,
    content    TEXT NOT NULL DEFAULT '',
    items      TEXT NOT NULL,
    source     TEXT,
    created_at INTEGER NOT NULL,
    UNIQUE (memory_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_entries_memory_seq ON entries (memory_id, seq);

-- Shared discussion transcript: one row per turn, in speaking order.
CREATE TABLE IF NOT EXISTS turns (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    round         INTEGER NOT NULL,
    turn          INTEGER NOT NULL,
    speaker       TEXT NOT NULL,
    model         TEXT NOT NULL,
    content       TEXT NOT NULL,
    passed        INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    UNIQUE (round, turn)
);

CREATE INDEX IF NOT EXISTS idx_turns_round ON turns (round, turn);

-- Every output item a speak turn produced (reasoning, tool calls, tool
-- outputs, messages), in the order it was produced. The web transcript renders
-- these ordered by time rather than only the completed turns.
CREATE TABLE IF NOT EXISTS speak_entries (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    round         INTEGER NOT NULL,
    speaker       TEXT NOT NULL,
    model         TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL,
    content       TEXT NOT NULL,
    created_at    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_speak_entries_time ON speak_entries (created_at, id);

-- Research assistant audit log: every tool call, its caller, and its output.
CREATE TABLE IF NOT EXISTS research_log (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    caller        TEXT NOT NULL,
    question      TEXT NOT NULL,
    answer        TEXT,
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens  INTEGER NOT NULL DEFAULT 0,
    cost          REAL NOT NULL DEFAULT 0,
    error         TEXT,
    created_at    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_research_log_caller ON research_log (caller, created_at);

-- Every debate model call (a persona speaking, or comprehending what it heard),
-- with its token usage and cost. Research-assistant calls are logged separately
-- in research_log, so the two tables together cover every paid model call.
CREATE TABLE IF NOT EXISTS model_calls (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    round         INTEGER NOT NULL DEFAULT 0,
    speaker       TEXT NOT NULL,
    model         TEXT NOT NULL,
    purpose       TEXT NOT NULL,
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens  INTEGER NOT NULL DEFAULT 0,
    cost          REAL NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_model_calls_round ON model_calls (round, created_at);
