package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"AetherGrid/controlPlane/internal/audit"
)

// AuditRepository is the SQLite-backed append-only audit event store.
type AuditRepository struct {
	db *sql.DB
}

// NewAuditRepository constructs an audit repository sharing the control
// plane's single database handle.
func NewAuditRepository(db *sql.DB) *AuditRepository {
	return &AuditRepository{db: db}
}

// Insert appends one audit event.
func (r *AuditRepository) Insert(ctx context.Context, event audit.Event) error {
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO audit_events (
			occurred_at, actor, actor_type, operation, resource,
			result, request_id, source, reason
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		formatTime(event.Time),
		event.Actor,
		event.ActorType,
		event.Operation,
		event.Resource,
		event.Result,
		event.RequestID,
		event.Source,
		event.Reason,
	)
	if err != nil {
		return fmt.Errorf("inserting audit event: %w", err)
	}
	return nil
}

// Count returns the number of stored audit events (used by tests).
func (r *AuditRepository) Count(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events`).Scan(&count)
	return count, err
}
