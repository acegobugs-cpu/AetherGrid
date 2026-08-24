-- 009_security.sql
-- Phase 10 security hardening: node credential lifecycle and the audit trail.
--
-- node_credentials stores ONLY the SHA-256 hash of every issued credential.
-- Plaintext tokens are shown exactly once in the API response that issues
-- them and are never persisted.

CREATE TABLE IF NOT EXISTS node_credentials (
    token_hash   TEXT PRIMARY KEY,
    node_id      TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('bootstrap', 'agent')),
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'used', 'revoked')),
    created_at   TEXT NOT NULL,
    expires_at   TEXT NOT NULL,
    used_at      TEXT,
    revoked_at   TEXT,
    last_used_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_node_credentials_node_id ON node_credentials(node_id);
CREATE INDEX IF NOT EXISTS idx_node_credentials_status ON node_credentials(status);

-- audit_events is append-only. It records who caused or authorized every
-- security-sensitive action, separate from application logs.
CREATE TABLE IF NOT EXISTS audit_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    occurred_at TEXT NOT NULL,
    actor       TEXT NOT NULL DEFAULT 'unknown',
    actor_type  TEXT NOT NULL DEFAULT 'unknown',
    operation   TEXT NOT NULL,
    resource    TEXT NOT NULL DEFAULT '',
    result      TEXT NOT NULL,
    request_id  TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT '',
    reason      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_audit_events_occurred_at ON audit_events(occurred_at);
CREATE INDEX IF NOT EXISTS idx_audit_events_operation ON audit_events(operation);

-- The bootstrap_token / wireguard_public_key / private_network_ip columns
-- from migrations 005/006 were never used by application code and are
-- plaintext-capable credential storage. Remove them so no code path can
-- start persisting secrets there again.
ALTER TABLE infrastructure DROP COLUMN bootstrap_token;
ALTER TABLE infrastructure DROP COLUMN wireguard_public_key;
ALTER TABLE infrastructure DROP COLUMN private_network_ip;
