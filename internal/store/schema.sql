-- Persona memories: one row per (discussion, persona).
CREATE TABLE IF NOT EXISTS memories (
    id             TEXT PRIMARY KEY,
    discussion_id  TEXT NOT NULL,
    persona        TEXT NOT NULL,
    common_prompt  TEXT NOT NULL,
    persona_prompt TEXT NOT NULL,
    created_at     INTEGER NOT NULL,
    UNIQUE (discussion_id, persona)
);

-- Memory entries: a persona's own turns and its understandings of what it heard.
-- content is the persona's own text (its answer or its understanding summary);
-- items is the full agent loop, kept for audit but never replayed into history.
CREATE TABLE IF NOT EXISTS entries (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    memory_id  TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
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
    discussion_id TEXT NOT NULL,
    round         INTEGER NOT NULL,
    turn          INTEGER NOT NULL,
    speaker       TEXT NOT NULL,
    model         TEXT NOT NULL,
    content       TEXT NOT NULL,
    passed        INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    UNIQUE (discussion_id, round, turn)
);

CREATE INDEX IF NOT EXISTS idx_turns_discussion_round ON turns (discussion_id, round, turn);

-- Research assistant audit log: every tool call, its caller, and its output.
CREATE TABLE IF NOT EXISTS research_log (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    caller        TEXT NOT NULL,
    question      TEXT NOT NULL,
    context       TEXT,
    answer        TEXT,
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens  INTEGER NOT NULL DEFAULT 0,
    cost          REAL NOT NULL DEFAULT 0,
    error         TEXT,
    created_at    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_research_log_caller ON research_log (caller, created_at);
