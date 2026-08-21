package repository

import (
	"context"

	"AetherGrid/controlPlane/internal/domain"
)

// ClusterRepository is the persistence interface for Kubernetes clusters.
type ClusterRepository interface {
	// Create persists a new cluster definition. It returns ErrConflict if a
	// cluster with the same name already exists.
	Create(ctx context.Context, cluster *domain.Cluster) error
	// GetByID returns a single cluster by its UUID.
	GetByID(ctx context.Context, id string) (*domain.Cluster, error)
	// GetByName returns a single cluster by its unique name.
	GetByName(ctx context.Context, name string) (*domain.Cluster, error)
	// GetAll returns every registered cluster.
	GetAll(ctx context.Context) ([]*domain.Cluster, error)
	// Update persists changes to an existing cluster.
	Update(ctx context.Context, cluster *domain.Cluster) error
	// Delete removes a cluster by its UUID.
	Delete(ctx context.Context, id string) error
}

// ClusterOperationRepository is the persistence interface for cluster bootstrap
// operations.
type ClusterOperationRepository interface {
	// CreateOperation persists a new cluster operation.
	CreateOperation(ctx context.Context, op *domain.ClusterOperation) error
	// GetClusterOperationByID returns a single operation by its UUID.
	GetClusterOperationByID(ctx context.Context, id string) (*domain.ClusterOperation, error)
	// ListOperationsByCluster returns operations for one cluster, newest first.
	ListOperationsByCluster(ctx context.Context, clusterID string) ([]*domain.ClusterOperation, error)
	// UpdateOperation persists changes to an existing operation.
	UpdateOperation(ctx context.Context, op *domain.ClusterOperation) error
	// FailInFlight marks every non-terminal cluster operation as failed.
	FailInFlight(ctx context.Context, reason string) (int, error)
}
