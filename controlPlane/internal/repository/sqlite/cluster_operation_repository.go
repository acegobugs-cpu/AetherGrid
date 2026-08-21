package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"AetherGrid/controlPlane/internal/domain"
	"AetherGrid/controlPlane/internal/repository"
)

// clusterOperationRepository is the SQLite-backed implementation of repository.ClusterOperationRepository.
type clusterOperationRepository struct {
	db *sql.DB
}

// NewClusterOperationRepository returns a clusterOperationRepository backed by the
// given SQLite database connection.
func NewClusterOperationRepository(db *sql.DB) *clusterOperationRepository {
	return &clusterOperationRepository{db: db}
}

// CreateOperation persists a new cluster operation.
func (r *clusterOperationRepository) CreateOperation(ctx context.Context, op *domain.ClusterOperation) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO cluster_operations (
			id, cluster_id, type, status, started_at, completed_at, error, current_step, succeeded_steps, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		op.ID,
		op.ClusterID,
		string(op.Type),
		string(op.Status),
		nil,
		nil,
		op.Error,
		op.CurrentStep,
		strings.Join(op.SucceededSteps, ","),
		time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	)
	if err != nil {
		return fmt.Errorf("creating cluster operation %s: %w", op.ID, err)
	}
	return nil
}

// GetClusterOperationByID returns a single operation by its UUID.
func (r *clusterOperationRepository) GetClusterOperationByID(ctx context.Context, id string) (*domain.ClusterOperation, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, cluster_id, type, status, started_at, completed_at, error, current_step, succeeded_steps, created_at, updated_at
		FROM cluster_operations WHERE id = ?`, id)

	var op domain.ClusterOperation
	var startedAt, completedAt, createdAt, updatedAt string
	var succeededSteps string
	err := row.Scan(
		&op.ID,
		&op.ClusterID,
		&op.Type,
		&op.Status,
		&startedAt,
		&completedAt,
		&op.Error,
		&op.CurrentStep,
		&succeededSteps,
		&createdAt,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying cluster operation %s: %w", id, err)
	}

	// Parse succeeded steps from comma-separated string
	op.SucceededSteps = strings.Split(succeededSteps, ",")
	if len(op.SucceededSteps) == 1 && op.SucceededSteps[0] == "" {
		op.SucceededSteps = nil
	}

	op.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parsing created_at: %w", err)
	}
	op.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parsing updated_at: %w", err)
	}

	return &op, nil
}

// FailInFlight marks every non-terminal cluster operation as failed with the
// given reason. It is used on control-plane restart so operations interrupted
// by a crash are never left looking active.
func (r *clusterOperationRepository) FailInFlight(ctx context.Context, reason string) (int, error) {
	// Mark all non-terminal operations as failed
	result, err := r.db.ExecContext(ctx, `
		UPDATE cluster_operations SET
			status = 'FAILED',
			error = ?,
			completed_at = ?
		WHERE status != 'SUCCEEDED'
			AND status != 'FAILED'
			AND status != 'CANCELLED'`,
		reason,
		time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	)
	if err != nil {
		return 0, fmt.Errorf("failing in-flight cluster operations: %w", err)
	}

	if affected, err := result.RowsAffected(); err == nil {
		return int(affected), nil
	}
	return 0, nil
}

// UpdateOperation persists changes to an existing cluster operation.
func (r *clusterOperationRepository) UpdateOperation(ctx context.Context, op *domain.ClusterOperation) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE cluster_operations SET
			status = ?,
			error = ?,
			current_step = ?,
			succeeded_steps = ?,
			completed_at = ?,
			updated_at = ?
		WHERE id = ?`,
		string(op.Status),
		op.Error,
		op.CurrentStep,
		strings.Join(op.SucceededSteps, ","),
		time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		op.ID,
	)
	if err != nil {
		return fmt.Errorf("updating cluster operation %s: %w", op.ID, err)
	}
	return nil
}

// ListOperationsByCluster returns the operations for one cluster, newest first.
func (r *clusterOperationRepository) ListOperationsByCluster(ctx context.Context, clusterID string) ([]*domain.ClusterOperation, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, cluster_id, type, status, started_at, completed_at, error, current_step, succeeded_steps, created_at, updated_at
		FROM cluster_operations WHERE cluster_id = ? ORDER BY created_at DESC`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("listing cluster operations: %w", err)
	}
	defer rows.Close()

	var operations []*domain.ClusterOperation
	for rows.Next() {
		var op domain.ClusterOperation
		var startedAt, completedAt, createdAt, updatedAt string
		var succeededSteps string
		err := rows.Scan(
			&op.ID,
			&op.ClusterID,
			&op.Type,
			&op.Status,
			&startedAt,
			&completedAt,
			&op.Error,
			&op.CurrentStep,
			&succeededSteps,
			&createdAt,
			&updatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning cluster operation: %w", err)
		}

		// Parse succeeded steps from comma-separated string
		op.SucceededSteps = strings.Split(succeededSteps, ",")
		if len(op.SucceededSteps) == 1 && op.SucceededSteps[0] == "" {
			op.SucceededSteps = nil
		}

		op.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return nil, fmt.Errorf("parsing created_at: %w", err)
		}
		op.UpdatedAt, err = parseTime(updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parsing updated_at: %w", err)
		}
		operations = append(operations, &op)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating cluster operations: %w", err)
	}
	return operations, nil
}