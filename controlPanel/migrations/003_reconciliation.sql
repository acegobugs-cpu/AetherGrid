-- 003_reconciliation.sql
-- Reconciliation engine (Phase 3).
-- Adds reconciliation metadata to nodes and a lightweight operational history.

ALTER TABLE nodes ADD COLUMN last_reconciliation TEXT;
ALTER TABLE nodes ADD COLUMN last_successful_reconciliation TEXT;
ALTER TABLE nodes ADD COLUMN last_reconciliation_result TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN last_reconciliation_action TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN last_reconciliation_error TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN last_reconciliation_deadline TEXT;
ALTER TABLE nodes ADD COLUMN reconciliation_attempts INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS reconciliation_events (
    id           TEXT PRIMARY KEY,
    node_id      TEXT NOT NULL,
    started_at   TEXT NOT NULL,
    completed_at TEXT NOT NULL,
    result       TEXT NOT NULL,
    action       TEXT NOT NULL DEFAULT '',
    attempt      INTEGER NOT NULL DEFAULT 0,
    error        TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_reconciliation_events_node
    ON reconciliation_events (node_id, started_at);