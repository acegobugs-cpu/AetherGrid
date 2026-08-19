// Package service contains the business logic of the control plane. Services
// depend on the repository abstraction and are independent of both HTTP and
// the concrete database implementation.
package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
	"github.com/acegobugs-cpu/AetherGrid/internal/repository"

	"github.com/google/uuid"
)

// ValidationError describes a request that does not satisfy domain rules.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// NodeService handles the node lifecycle: creation, retrieval, listing,
// deletion and state updates.
type NodeService struct {
	repo repository.NodeRepository
}

// NewNodeService constructs a NodeService backed by the given repository.
func NewNodeService(repo repository.NodeRepository) *NodeService {
	return &NodeService{repo: repo}
}

// CreateNodeInput carries the operator-supplied fields for a new node.
type CreateNodeInput struct {
	Name              string
	Location          string
	IPAddress         string
	KubernetesEnabled bool
	WireGuardEnabled  bool
}

// Create validates the input, assigns a UUID and initial state, and persists
// the new node.
func (s *NodeService) Create(ctx context.Context, input CreateNodeInput) (*domain.Node, error) {
	if err := ValidateNewNode(input); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	node := &domain.Node{
		ID:                uuid.NewString(),
		Name:              strings.TrimSpace(input.Name),
		Location:          strings.TrimSpace(input.Location),
		IPAddress:         strings.TrimSpace(input.IPAddress),
		Status:            domain.InitialStatus,
		DesiredStatus:     domain.DesiredInitialStatus,
		KubernetesEnabled: input.KubernetesEnabled,
		WireGuardEnabled:  input.WireGuardEnabled,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if err := s.repo.Create(ctx, node); err != nil {
		return nil, err
	}
	return node, nil
}

// Get returns a single node by ID.
func (s *NodeService) Get(ctx context.Context, id string) (*domain.Node, error) {
	return s.repo.GetByID(ctx, id)
}

// List returns all registered nodes.
func (s *NodeService) List(ctx context.Context) ([]*domain.Node, error) {
	return s.repo.GetAll(ctx)
}

// Delete removes a node by ID.
func (s *NodeService) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

// SetDesiredStatus updates the desired status of a node.
func (s *NodeService) SetDesiredStatus(ctx context.Context, id string, status domain.NodeStatus) (*domain.Node, error) {
	if !status.Valid() {
		return nil, &ValidationError{Message: fmt.Sprintf("invalid status %q", status)}
	}

	node, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	node.DesiredStatus = status
	node.UpdatedAt = time.Now().UTC()

	if err := s.repo.Update(ctx, node); err != nil {
		return nil, err
	}
	return node, nil
}

// ValidateNewNode checks the operator-supplied fields required to register a
// node.
func ValidateNewNode(input CreateNodeInput) error {
	if strings.TrimSpace(input.Name) == "" {
		return &ValidationError{Message: "name is required"}
	}
	if input.IPAddress != "" && net.ParseIP(input.IPAddress) == nil {
		return &ValidationError{Message: fmt.Sprintf("invalid ip_address %q", input.IPAddress)}
	}
	return nil
}

// IsNotFound reports whether err represents a missing node.
func IsNotFound(err error) bool {
	return errors.Is(err, repository.ErrNotFound)
}

// IsConflict reports whether err represents a uniqueness conflict.
func IsConflict(err error) bool {
	return errors.Is(err, repository.ErrConflict)
}
