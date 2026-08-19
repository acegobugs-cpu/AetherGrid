package repository

import (
	"context"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
)

// ReconciliationHistoryRepository is the persistence interface for the
// lightweight reconciliation history. Current node state remains the
// authoritative source; history exists for observability and debugging.
type ReconciliationHistoryRepository interface {
	// Create persists a reconciliation event.
	Create(ctx context.Context, event *domain.ReconciliationEvent) error
	// GetLatest returns the most recent event for a node, or nil when there
	// are none.
	GetLatest(ctx context.Context, nodeID string) (*domain.ReconciliationEvent, error)
	// ListByNode returns the most recent events for a node, newest first,
	// limited to limit entries.
	ListByNode(ctx context.Context, nodeID string, limit int) ([]*domain.ReconciliationEvent, error)
}
