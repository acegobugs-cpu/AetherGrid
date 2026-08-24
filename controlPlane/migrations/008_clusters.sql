-- Phase 9 follow-up: the clusters and cluster_operations tables used by the
-- Phase 8 cluster bootstrap repositories were missing from the migration
-- history. Recovery preconditions query them, so they must exist.

CREATE TABLE IF NOT EXISTS clusters (
	id                 TEXT PRIMARY KEY,
	name               TEXT NOT NULL UNIQUE,
	state              TEXT NOT NULL DEFAULT 'PENDING',
	kubernetes_version TEXT NOT NULL DEFAULT '',
	control_plane_node TEXT NOT NULL DEFAULT '',
	created_at         TEXT NOT NULL,
	updated_at         TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS cluster_operations (
	id              TEXT PRIMARY KEY,
	cluster_id      TEXT NOT NULL,
	type            TEXT NOT NULL,
	status          TEXT NOT NULL,
	started_at      TEXT,
	completed_at    TEXT,
	error           TEXT NOT NULL DEFAULT '',
	current_step    TEXT NOT NULL DEFAULT '',
	succeeded_steps TEXT NOT NULL DEFAULT '',
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_cluster_operations_cluster ON cluster_operations (cluster_id);
