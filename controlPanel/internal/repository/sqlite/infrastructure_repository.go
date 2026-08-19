package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
	"github.com/acegobugs-cpu/AetherGrid/internal/repository"
)

// InfrastructureRepository is the SQLite-backed implementation of the
// infrastructure and operation repositories. It shares the database handle
// opened by NodeRepository.
type InfrastructureRepository struct {
	db *sql.DB
}

// NewInfrastructureRepository constructs an infrastructure repository sharing
// the given database handle.
func NewInfrastructureRepository(db *sql.DB) *InfrastructureRepository {
	return &InfrastructureRepository{db: db}
}

func nowUTC() string {
	return time.Now().UTC().Format(TimeLayout)
}

// Create inserts a new infrastructure deployment.
func (r *InfrastructureRepository) Create(ctx context.Context, infra *domain.Infrastructure) error {
	nodes, err := json.Marshal(infra.Status.Nodes)
	if err != nil {
		return fmt.Errorf("marshalling infrastructure nodes: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO infrastructure (
			id, name, node_count, cpu, memory_mb, disk_gb, image, provider,
			phase, last_operation, error, nodes, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		infra.ID,
		infra.Spec.Name,
		infra.Spec.NodeCount,
		infra.Spec.CPU,
		infra.Spec.MemoryMB,
		infra.Spec.DiskGB,
		infra.Spec.Image,
		infra.Spec.Provider,
		string(infra.Status.Phase),
		infra.Status.LastOperation,
		infra.Status.Error,
		string(nodes),
		formatTime(infra.CreatedAt),
		formatTime(infra.UpdatedAt),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: name %q", repository.ErrConflict, infra.Spec.Name)
		}
		return fmt.Errorf("inserting infrastructure %q: %w", infra.ID, err)
	}
	return nil
}

// GetByID returns a single infrastructure deployment by UUID.
func (r *InfrastructureRepository) GetByID(ctx context.Context, id string) (*domain.Infrastructure, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, node_count, cpu, memory_mb, disk_gb, image, provider,
		       phase, last_operation, error, nodes, created_at, updated_at
		FROM infrastructure WHERE id = ?`, id)
	infra, err := scanInfrastructure(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying infrastructure %q: %w", id, err)
	}
	return infra, nil
}

// GetByName returns a single infrastructure deployment by its unique name.
func (r *InfrastructureRepository) GetByName(ctx context.Context, name string) (*domain.Infrastructure, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, node_count, cpu, memory_mb, disk_gb, image, provider,
		       phase, last_operation, error, nodes, created_at, updated_at
		FROM infrastructure WHERE name = ?`, name)
	infra, err := scanInfrastructure(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying infrastructure %q: %w", name, err)
	}
	return infra, nil
}

// GetAll returns every infrastructure deployment, oldest first.
func (r *InfrastructureRepository) GetAll(ctx context.Context) ([]*domain.Infrastructure, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, node_count, cpu, memory_mb, disk_gb, image, provider,
		       phase, last_operation, error, nodes, created_at, updated_at
		FROM infrastructure ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("querying infrastructure: %w", err)
	}
	defer rows.Close()

	var infrastructures []*domain.Infrastructure
	for rows.Next() {
		infra, err := scanInfrastructure(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning infrastructure row: %w", err)
		}
		infrastructures = append(infrastructures, infra)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating infrastructure: %w", err)
	}
	return infrastructures, nil
}

// Update persists changes to an existing infrastructure deployment.
func (r *InfrastructureRepository) Update(ctx context.Context, infra *domain.Infrastructure) error {
	nodes, err := json.Marshal(infra.Status.Nodes)
	if err != nil {
		return fmt.Errorf("marshalling infrastructure nodes: %w", err)
	}

	result, err := r.db.ExecContext(ctx, `
		UPDATE infrastructure SET
			node_count = ?, cpu = ?, memory_mb = ?, disk_gb = ?, image = ?,
			provider = ?, phase = ?, last_operation = ?, error = ?, nodes = ?,
			updated_at = ?
		WHERE id = ?`,
		infra.Spec.NodeCount,
		infra.Spec.CPU,
		infra.Spec.MemoryMB,
		infra.Spec.DiskGB,
		infra.Spec.Image,
		infra.Spec.Provider,
		string(infra.Status.Phase),
		infra.Status.LastOperation,
		infra.Status.Error,
		string(nodes),
		formatTime(infra.UpdatedAt),
		infra.ID,
	)
	if err != nil {
		return fmt.Errorf("updating infrastructure %q: %w", infra.ID, err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return repository.ErrNotFound
	}
	return nil
}

// Delete removes an infrastructure deployment by UUID.
func (r *InfrastructureRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM infrastructure WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting infrastructure %q: %w", id, err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func scanInfrastructure(scanner rowScanner) (*domain.Infrastructure, error) {
	var (
		id            string
		name          string
		nodeCount     int
		cpu           int
		memoryMB      int
		diskGB        int
		image         string
		provider      string
		phase         string
		lastOperation string
		errorMessage  string
		nodesJSON     string
		createdAt     string
		updatedAt     string
	)
	if err := scanner.Scan(
		&id, &name, &nodeCount, &cpu, &memoryMB, &diskGB, &image, &provider,
		&phase, &lastOperation, &errorMessage, &nodesJSON, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}

	created, err := parseTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parsing created_at %q: %w", createdAt, err)
	}
	updated, err := parseTime(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parsing updated_at %q: %w", updatedAt, err)
	}

	var nodes []domain.InfrastructureNode
	if nodesJSON != "" {
		if err := json.Unmarshal([]byte(nodesJSON), &nodes); err != nil {
			return nil, fmt.Errorf("parsing nodes %q: %w", nodesJSON, err)
		}
	}

	return &domain.Infrastructure{
		ID: id,
		Spec: domain.InfrastructureSpec{
			Name:      name,
			NodeCount: nodeCount,
			CPU:       cpu,
			MemoryMB:  memoryMB,
			DiskGB:    diskGB,
			Image:     image,
			Provider:  provider,
		},
		Status: domain.InfrastructureStatus{
			Phase:         domain.InfrastructurePhase(phase),
			Nodes:         nodes,
			LastOperation: lastOperation,
			Error:         errorMessage,
		},
		CreatedAt: created,
		UpdatedAt: updated,
	}, nil
}

// Create persists a new provisioning operation.
func (r *InfrastructureRepository) CreateOperation(ctx context.Context, op *domain.InfrastructureOperation) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO infrastructure_operations (
			id, infrastructure_id, type, status, changes_create, changes_modify,
			changes_destroy, started_at, completed_at, error, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		op.ID,
		op.InfrastructureID,
		string(op.Type),
		string(op.Status),
		op.Changes.ToCreate,
		op.Changes.ToModify,
		op.Changes.ToDestroy,
		nullableTime(op.StartedAt),
		nullableTime(op.CompletedAt),
		op.Error,
		formatTime(op.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("inserting operation %q: %w", op.ID, err)
	}
	return nil
}

// GetOperationByID returns a single operation by UUID.
func (r *InfrastructureRepository) GetOperationByID(ctx context.Context, id string) (*domain.InfrastructureOperation, error) {
	row := r.db.QueryRowContext(ctx, operationSelect+" WHERE id = ?", id)
	op, err := scanInfrastructureOperation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying operation %q: %w", id, err)
	}
	return op, nil
}

// ListOperationsByInfrastructure returns the operations for one deployment,
// newest first.
func (r *InfrastructureRepository) ListOperationsByInfrastructure(ctx context.Context, infrastructureID string) ([]*domain.InfrastructureOperation, error) {
	rows, err := r.db.QueryContext(ctx,
		operationSelect+" WHERE infrastructure_id = ? ORDER BY created_at DESC", infrastructureID)
	if err != nil {
		return nil, fmt.Errorf("querying operations for %q: %w", infrastructureID, err)
	}
	defer rows.Close()

	var operations []*domain.InfrastructureOperation
	for rows.Next() {
		op, err := scanInfrastructureOperation(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning operation row: %w", err)
		}
		operations = append(operations, op)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating operations: %w", err)
	}
	return operations, nil
}

// UpdateOperation persists changes to an existing operation.
func (r *InfrastructureRepository) UpdateOperation(ctx context.Context, op *domain.InfrastructureOperation) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE infrastructure_operations SET
			status = ?, changes_create = ?, changes_modify = ?, changes_destroy = ?,
			started_at = ?, completed_at = ?, error = ?
		WHERE id = ?`,
		string(op.Status),
		op.Changes.ToCreate,
		op.Changes.ToModify,
		op.Changes.ToDestroy,
		nullableTime(op.StartedAt),
		nullableTime(op.CompletedAt),
		op.Error,
		op.ID,
	)
	if err != nil {
		return fmt.Errorf("updating operation %q: %w", op.ID, err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return repository.ErrNotFound
	}
	return nil
}

// FailInFlight marks every non-terminal operation as failed.
func (r *InfrastructureRepository) FailInFlight(ctx context.Context, reason string) (int, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE infrastructure_operations SET
			status = ?, completed_at = ?, error = ?
		WHERE status IN (?, ?)`,
		string(domain.OpFailed),
		nowUTC(),
		reason,
		string(domain.OpPending),
		string(domain.OpRunning),
	)
	if err != nil {
		return 0, fmt.Errorf("failing in-flight operations: %w", err)
	}
	affected, _ := result.RowsAffected()
	return int(affected), nil
}

const operationSelect = `
	SELECT id, infrastructure_id, type, status, changes_create, changes_modify,
	       changes_destroy, started_at, completed_at, error, created_at
	FROM infrastructure_operations`

func scanInfrastructureOperation(scanner rowScanner) (*domain.InfrastructureOperation, error) {
	var (
		id               string
		infrastructureID string
		opType           string
		status           string
		changesCreate    int
		changesModify    int
		changesDestroy   int
		startedAt        sql.NullString
		completedAt      sql.NullString
		errorMessage     string
		createdAt        string
	)
	if err := scanner.Scan(
		&id, &infrastructureID, &opType, &status, &changesCreate, &changesModify,
		&changesDestroy, &startedAt, &completedAt, &errorMessage, &createdAt,
	); err != nil {
		return nil, err
	}

	created, err := parseTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("parsing operation created_at %q: %w", createdAt, err)
	}

	op := &domain.InfrastructureOperation{
		ID:               id,
		InfrastructureID: infrastructureID,
		Type:             domain.OperationType(opType),
		Status:           domain.OperationStatus(status),
		Changes: domain.ChangeSummary{
			ToCreate:  changesCreate,
			ToModify:  changesModify,
			ToDestroy: changesDestroy,
		},
		Error:     errorMessage,
		CreatedAt: created,
	}
	if startedAt.Valid {
		value, err := parseTime(startedAt.String)
		if err != nil {
			return nil, fmt.Errorf("parsing operation started_at %q: %w", startedAt.String, err)
		}
		op.StartedAt = &value
	}
	if completedAt.Valid {
		value, err := parseTime(completedAt.String)
		if err != nil {
			return nil, fmt.Errorf("parsing operation completed_at %q: %w", completedAt.String, err)
		}
		op.CompletedAt = &value
	}
	return op, nil
}