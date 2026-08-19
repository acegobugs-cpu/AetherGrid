package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"

	"github.com/google/uuid"
)

// ReconciliationRepository is the SQLite-backed implementation of
// repository.ReconciliationHistoryRepository.
type ReconciliationRepository struct {
	db *sql.DB
}

// NewReconciliationRepository returns a reconciliation history repository
// sharing the given database handle.
func NewReconciliationRepository(db *sql.DB) *ReconciliationRepository {
	return &ReconciliationRepository{db: db}
}

// Create inserts a new reconciliation event.
func (r *ReconciliationRepository) Create(ctx context.Context, event *domain.ReconciliationEvent) error {
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO reconciliation_events (
			id, node_id, started_at, completed_at, result, action, attempt, error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.NodeID,
		formatTime(event.StartedAt),
		formatTime(event.CompletedAt),
		string(event.Result),
		event.Action,
		event.Attempt,
		event.Error,
	)
	if err != nil {
		return fmt.Errorf("inserting reconciliation event: %w", err)
	}
	return nil
}

// GetLatest returns the most recent event for a node, or nil when there are
// none.
func (r *ReconciliationRepository) GetLatest(ctx context.Context, nodeID string) (*domain.ReconciliationEvent, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, node_id, started_at, completed_at, result, action, attempt, error
		FROM reconciliation_events
		WHERE node_id = ?
		ORDER BY started_at DESC, completed_at DESC
		LIMIT 1`, nodeID)

	event, err := scanReconciliationEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying latest reconciliation event for node %q: %w", nodeID, err)
	}
	return event, nil
}

// ListByNode returns the most recent events for a node, newest first, limited
// to limit entries.
func (r *ReconciliationRepository) ListByNode(ctx context.Context, nodeID string, limit int) ([]*domain.ReconciliationEvent, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, node_id, started_at, completed_at, result, action, attempt, error
		FROM reconciliation_events
		WHERE node_id = ?
		ORDER BY started_at DESC, completed_at DESC
		LIMIT ?`, nodeID, limit)
	if err != nil {
		return nil, fmt.Errorf("querying reconciliation events for node %q: %w", nodeID, err)
	}
	defer rows.Close()

	var events []*domain.ReconciliationEvent
	for rows.Next() {
		event, err := scanReconciliationEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning reconciliation event row: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating reconciliation events: %w", err)
	}
	return events, nil
}

func scanReconciliationEvent(scanner rowScanner) (*domain.ReconciliationEvent, error) {
	var (
		id          string
		nodeID      string
		startedAt   string
		completedAt string
		result      string
		action      string
		attempt     int
		errorText   string
	)
	if err := scanner.Scan(&id, &nodeID, &startedAt, &completedAt, &result, &action, &attempt, &errorText); err != nil {
		return nil, err
	}

	started, err := parseTime(startedAt)
	if err != nil {
		return nil, fmt.Errorf("parsing started_at %q: %w", startedAt, err)
	}
	completed, err := parseTime(completedAt)
	if err != nil {
		return nil, fmt.Errorf("parsing completed_at %q: %w", completedAt, err)
	}

	return &domain.ReconciliationEvent{
		ID:          id,
		NodeID:      nodeID,
		StartedAt:   started,
		CompletedAt: completed,
		Result:      domain.ReconciliationStatus(result),
		Action:      action,
		Attempt:     attempt,
		Error:       errorText,
	}, nil
}
