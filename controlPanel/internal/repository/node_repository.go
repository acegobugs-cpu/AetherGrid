// Package repository defines the persistence abstraction used by the service
// layer. Application code depends on the NodeRepository interface rather than
// on any concrete database implementation, allowing the storage backend to be
// swapped (for example to PostgreSQL) without touching business logic.
package repository

import (
	"context"
	"errors"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
)

// ErrNotFound is returned by repository methods when the requested node does
// not exist.
var ErrNotFound = errors.New("node not found")

// ErrConflict is returned when an operation violates a uniqueness constraint,
// such as registering a node with a name that already exists.
var ErrConflict = errors.New("node already exists")

// NodeRepository is the persistence interface for nodes.
type NodeRepository interface {
	// Create persists a new node. It returns ErrConflict if a node with the
	// same name already exists.
	Create(ctx context.Context, node *domain.Node) error
	// GetByID returns a single node by its UUID. It returns ErrNotFound if no
	// such node exists.
	GetByID(ctx context.Context, id string) (*domain.Node, error)
	// GetAll returns every registered node.
	GetAll(ctx context.Context) ([]*domain.Node, error)
	// Update persists changes to an existing node. It returns ErrNotFound if
	// the node no longer exists.
	Update(ctx context.Context, node *domain.Node) error
	// Delete removes a node by its UUID. It returns ErrNotFound if no such
	// node exists.
	Delete(ctx context.Context, id string) error
}
