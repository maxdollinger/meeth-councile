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
CREATE TABLE IF NOT EXISTS entries (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    memory_id  TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    seq        INTEGER NOT NULL,
    kind       TEXT NOT NULL,
    speaker    TEXT NOT NULL,
    items      TEXT NOT NULL,
    source     TEXT,
    created_at INTEGER NOT NULL,
    UNIQUE (memory_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_entries_memory_seq ON entries (memory_id, seq);

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
