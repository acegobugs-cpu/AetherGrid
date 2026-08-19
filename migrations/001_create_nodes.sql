-- 001_create_nodes.sql
-- Initial schema for the AETHER-GRID control plane.

CREATE TABLE IF NOT EXISTS nodes (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL UNIQUE,
    status              TEXT NOT NULL,
    desired_status      TEXT NOT NULL,
    location            TEXT NOT NULL DEFAULT '',
    ip_address          TEXT NOT NULL DEFAULT '',
    kubernetes_enabled  INTEGER NOT NULL DEFAULT 0,
    wireguard_enabled   INTEGER NOT NULL DEFAULT 0,
    last_heartbeat      TEXT,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);