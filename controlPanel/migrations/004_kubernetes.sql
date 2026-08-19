-- Phase 4: Kubernetes integration state for nodes.
-- The node's Kubernetes desired state (kubernetes_minimum_ready_nodes) and the
-- most recent Kubernetes state observed by the agent are stored alongside the
-- existing node fields so the reconciliation engine can detect drift between
-- desired and actual Kubernetes state.

ALTER TABLE nodes ADD COLUMN kubernetes_minimum_ready_nodes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN kubernetes_available INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN kubernetes_status TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN kubernetes_version TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN kubernetes_node_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN kubernetes_ready_nodes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN kubernetes_not_ready_nodes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN kubernetes_total_pods INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN kubernetes_running_pods INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN kubernetes_failed_pods INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN kubernetes_reported_at TEXT;