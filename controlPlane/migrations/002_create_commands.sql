-- 002_create_commands.sql
-- Command queue for control-plane to edge-agent instructions.
-- Added during Phase 2 (Edge Node Agent) for agent command dispatch.

CREATE TABLE IF NOT EXISTS commands (
    id         TEXT PRIMARY KEY,
    node_id    TEXT NOT NULL,
    type       TEXT NOT NULL,
    parameters TEXT NOT NULL DEFAULT '{}',
    status     TEXT NOT NULL DEFAULT 'PENDING',
    result     TEXT,
    error      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_commands_node_status
    ON commands (node_id, status);