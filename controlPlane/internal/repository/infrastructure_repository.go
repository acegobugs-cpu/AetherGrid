package repository

import (
	"context"

	"AetherGrid/controlPlane/internal/domain"
)

// InfrastructureRepository is the persistence interface for infrastructure
// deployments.
type InfrastructureRepository interface {
	// Create persists a new infrastructure deployment. It returns ErrConflict
	// if a deployment with the same name already exists.
	Create(ctx context.Context, infra *domain.Infrastructure) error
	// GetByID returns a single deployment by its UUID. It returns ErrNotFound
	// if no such deployment exists.
	GetByID(ctx context.Context, id string) (*domain.Infrastructure, error)
	// GetByName returns a single deployment by its unique name.
	GetByName(ctx context.Context, name string) (*domain.Infrastructure, error)
	// GetAll returns every registered deployment.
	GetAll(ctx context.Context) ([]*domain.Infrastructure, error)
	// Update persists changes to an existing deployment. It returns
	// ErrNotFound if the deployment no longer exists.
	Update(ctx context.Context, infra *domain.Infrastructure) error
	// Delete removes a deployment by its UUID. It returns ErrNotFound if no
	// such deployment exists.
	Delete(ctx context.Context, id string) error
}

// InfrastructureOperationRepository is the persistence interface for
// provisioning operations. Method names are distinct from the deployment
// repository because both interfaces are implemented by a single SQLite
// struct.
type InfrastructureOperationRepository interface {
	// CreateOperation persists a new operation.
	CreateOperation(ctx context.Context, op *domain.InfrastructureOperation) error
	// GetOperationByID returns a single operation by its UUID. It returns
	// ErrNotFound if no such operation exists.
	GetOperationByID(ctx context.Context, id string) (*domain.InfrastructureOperation, error)
	// ListOperationsByInfrastructure returns the operations for one
	// deployment, newest first.
	ListOperationsByInfrastructure(ctx context.Context, infrastructureID string) ([]*domain.InfrastructureOperation, error)
	// UpdateOperation persists changes to an existing operation.
	UpdateOperation(ctx context.Context, op *domain.InfrastructureOperation) error
	// FailInFlight marks every non-terminal operation as failed with the given
	// reason. It is used on control-plane restart so operations interrupted by
	// a crash are never left looking active.
	FailInFlight(ctx context.Context, reason string) (int, error)
}
