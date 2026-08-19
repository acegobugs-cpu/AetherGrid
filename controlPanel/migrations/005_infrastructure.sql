-- Phase 6: infrastructure provisioning. Stores declarative infrastructure
-- definitions and their lifecycle operations. The domain model is independent
-- of Terraform; only the provisioner layer knows about Terraform.

CREATE TABLE IF NOT EXISTS infrastructure (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL UNIQUE,
    node_count        INTEGER NOT NULL,
    cpu               INTEGER NOT NULL,
    memory_mb         INTEGER NOT NULL,
    disk_gb           INTEGER NOT NULL,
    image             TEXT NOT NULL,
    provider          TEXT NOT NULL,
    phase             TEXT NOT NULL,
    last_operation    TEXT NOT NULL DEFAULT '',
    error             TEXT NOT NULL DEFAULT '',
    nodes             TEXT NOT NULL DEFAULT '[]',
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    bootstrap_phase   TEXT NOT NULL DEFAULT 'PENDING',
    bootstrap_token   TEXT NOT NULL DEFAULT '',
    wireguard_public_key TEXT NOT NULL DEFAULT '',
    private_network_ip TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS infrastructure_operations (
    id                TEXT PRIMARY KEY,
    infrastructure_id TEXT NOT NULL REFERENCES infrastructure(id) ON DELETE CASCADE,
    type              TEXT NOT NULL,
    status            TEXT NOT NULL,
    changes_create    INTEGER NOT NULL DEFAULT 0,
    changes_modify    INTEGER NOT NULL DEFAULT 0,
    changes_destroy   INTEGER NOT NULL DEFAULT 0,
    started_at        TEXT,
    completed_at      TEXT,
    error             TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_infrastructure_operations_infra
    ON infrastructure_operations(infrastructure_id);