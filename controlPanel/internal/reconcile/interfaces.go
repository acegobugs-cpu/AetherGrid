package reconcile

import (
	"context"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
)

// NodeRepository is the persistence interface the reconciliation engine needs
// to observe nodes and persist its metadata. It is the domain repository
// interface, declared here so the engine depends on an abstraction.
type NodeRepository interface {
	GetByID(ctx context.Context, id string) (*domain.Node, error)
	GetAll(ctx context.Context) ([]*domain.Node, error)
	// UpdateReconciliation persists only the reconciliation metadata of a node,
	// leaving status, heartbeat and desired state untouched so the engine can
	// never clobber concurrent observation updates.
	UpdateReconciliation(ctx context.Context, node *domain.Node) error
}

// ReconciliationHistoryRepository persists the lightweight reconciliation
// history rows.
type ReconciliationHistoryRepository interface {
	Create(ctx context.Context, event *domain.ReconciliationEvent) error
}

// HistoryWriter is a function that persists a reconciliation event; it is an
// optional hook on the reconciler for tests.
type HistoryWriter func(ctx context.Context, event *domain.ReconciliationEvent) error
