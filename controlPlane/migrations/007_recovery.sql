-- Phase 9: Autonomous Infrastructure Reconciliation and Recovery.
-- Adds explicit node role plus persistent recovery state tracking so recovery
-- progress survives control-plane restarts.

ALTER TABLE nodes ADD COLUMN role TEXT NOT NULL DEFAULT '';

ALTER TABLE nodes ADD COLUMN recovery_state TEXT NOT NULL DEFAULT 'NOT_REQUIRED';

ALTER TABLE nodes ADD COLUMN recovery_failure TEXT NOT NULL DEFAULT '';

ALTER TABLE nodes ADD COLUMN recovery_attempts INTEGER NOT NULL DEFAULT 0;

ALTER TABLE nodes ADD COLUMN last_recovery_at TEXT;

ALTER TABLE nodes ADD COLUMN next_retry_at TEXT;

ALTER TABLE nodes ADD COLUMN failure_streak INTEGER NOT NULL DEFAULT 0;
