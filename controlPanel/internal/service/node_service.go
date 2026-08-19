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
	// notify is invoked when a node's state changes so the reconciliation
	// engine can reconcile immediately rather than waiting for the next sweep.
	notify func(nodeID string)
}

// NewNodeService constructs a NodeService backed by the given repository.
func NewNodeService(repo repository.NodeRepository) *NodeService {
	return &NodeService{repo: repo}
}

// SetReconcileNotifier registers the callback invoked whenever a node's
// desired or actual state changes.
func (s *NodeService) SetReconcileNotifier(notify func(nodeID string)) {
	s.notify = notify
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
	if s.notify != nil {
		s.notify(node.ID)
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

// UpdateStatus records agent-reported actual state for a node. It updates the
// node's observed status and, when provided, its IP address, and refreshes the
// heartbeat timestamp since a state report also proves liveness.
func (s *NodeService) UpdateStatus(ctx context.Context, id string, status domain.NodeStatus, ipAddress string) (*domain.Node, error) {
	if !status.Valid() {
		return nil, &ValidationError{Message: fmt.Sprintf("invalid status %q", status)}
	}

	node, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	node.Status = status
	node.UpdatedAt = now
	node.LastHeartbeat = &now
	if ip := strings.TrimSpace(ipAddress); ip != "" {
		if net.ParseIP(ip) == nil {
			return nil, &ValidationError{Message: fmt.Sprintf("invalid ip_address %q", ip)}
		}
		node.IPAddress = ip
	}

	if err := s.repo.Update(ctx, node); err != nil {
		return nil, err
	}
	if s.notify != nil {
		s.notify(id)
	}
	return node, nil
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
	if s.notify != nil {
		s.notify(id)
	}
	return node, nil
}

// SetDesiredState updates the structured desired state of a node. Only the
// declared fields are overwritten; a zero-status input leaves the status
// untouched so partial updates are safe.
func (s *NodeService) SetDesiredState(ctx context.Context, id string, desired domain.DesiredState) (*domain.Node, error) {
	node, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if desired.Status != "" {
		if !desired.Status.Valid() {
			return nil, &ValidationError{Message: fmt.Sprintf("invalid status %q", desired.Status)}
		}
		node.DesiredStatus = desired.Status
	}
	node.KubernetesEnabled = desired.KubernetesEnabled
	node.WireGuardEnabled = desired.WireGuardEnabled
	node.UpdatedAt = time.Now().UTC()

	if err := s.repo.Update(ctx, node); err != nil {
		return nil, err
	}
	if s.notify != nil {
		s.notify(id)
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
