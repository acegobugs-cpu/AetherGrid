-- Phase 7: Node networking and bootstrap.
-- Adds WireGuard identity, bootstrap state, and bootstrap token to infrastructure.

CREATE TABLE IF NOT EXISTS infrastructure (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL UNIQUE,
    node_count     INTEGER NOT NULL,
    cpu            INTEGER NOT NULL,
    memory_mb      INTEGER NOT NULL,
    disk_gb        INTEGER NOT NULL,
    image          TEXT NOT NULL,
    provider       TEXT NOT NULL,
    phase          TEXT NOT NULL,
    last_operation TEXT NOT NULL DEFAULT '',
    error          TEXT NOT NULL DEFAULT '',
    nodes          TEXT NOT NULL DEFAULT '[]',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    bootstrap_phase TEXT NOT NULL DEFAULT 'PENDING',
    bootstrap_token TEXT NOT NULL DEFAULT '',
    wireguard_public_key TEXT NOT NULL DEFAULT '',
    private_network_ip TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_infrastructure_bootstrap_phase
    ON infrastructure(bootstrap_phase);