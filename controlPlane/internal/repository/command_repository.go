// Package repository defines the persistence abstraction used by the service
// layer. Application code depends on the NodeRepository and CommandRepository
// interfaces rather than on any concrete database implementation.
package repository

import (
	"context"

	"AetherGrid/controlPlane/internal/domain"
)

// CommandRepository is the persistence interface for agent commands.
type CommandRepository interface {
	// Create persists a new command.
	Create(ctx context.Context, command *domain.Command) error
	// GetByID returns a single command by its UUID. It returns ErrNotFound if
	// no such command exists.
	GetByID(ctx context.Context, id string) (*domain.Command, error)
	// ListByNode returns every command for a node, ordered oldest first.
	ListByNode(ctx context.Context, nodeID string) ([]*domain.Command, error)
	// ListPendingByNode returns every command for a node that has not yet
	// reached a terminal state, ordered oldest first.
	ListPendingByNode(ctx context.Context, nodeID string) ([]*domain.Command, error)
	// Update persists changes to an existing command.
	Update(ctx context.Context, command *domain.Command) error
}
