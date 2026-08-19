package service

import (
	"context"
	"time"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
	"github.com/acegobugs-cpu/AetherGrid/internal/repository"
)

// HeartbeatService processes node heartbeats. It records reachability by
// updating the heartbeat timestamps without overwriting desired state.
type HeartbeatService struct {
	repo repository.NodeRepository
	// notify is invoked when a heartbeat arrives so the reconciliation engine
	// can re-evaluate the node without waiting for the next sweep.
	notify func(nodeID string)
}

// NewHeartbeatService constructs a HeartbeatService backed by the given
// repository.
func NewHeartbeatService(repo repository.NodeRepository) *HeartbeatService {
	return &HeartbeatService{repo: repo}
}

// SetReconcileNotifier registers the callback invoked whenever a heartbeat is
// recorded.
func (s *HeartbeatService) SetReconcileNotifier(notify func(nodeID string)) {
	s.notify = notify
}

// Record updates the last_heartbeat and updated_at timestamps for the node
// with the given ID, returning the updated node. The desired state is left
// untouched. It returns repository.ErrNotFound if the node does not exist.
func (s *HeartbeatService) Record(ctx context.Context, id string) (*domain.Node, error) {
	node, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	node.LastHeartbeat = &now
	node.UpdatedAt = now

	if err := s.repo.Update(ctx, node); err != nil {
		return nil, err
	}
	if s.notify != nil {
		s.notify(id)
	}
	return node, nil
}
