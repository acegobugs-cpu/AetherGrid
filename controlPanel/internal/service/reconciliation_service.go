package service

import (
	"context"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
	"github.com/acegobugs-cpu/AetherGrid/internal/repository"
)

// Reconciliation results.
const (
	// ResultInSync indicates desired and actual state match.
	ResultInSync = "IN_SYNC"
	// ResultDriftDetected indicates desired and actual state differ.
	ResultDriftDetected = "DRIFT_DETECTED"
)

// ReconciliationResult is the outcome of comparing a node's desired state
// against its actual state.
type ReconciliationResult struct {
	NodeID       string            `json:"node_id"`
	DesiredState domain.NodeStatus `json:"desired_state"`
	ActualState  domain.NodeStatus `json:"actual_state"`
	Result       string            `json:"result"`
}

// ReconciliationService compares desired and actual state. Phase 1 only
// detects differences; it never performs recovery actions.
type ReconciliationService struct {
	repo repository.NodeRepository
}

// NewReconciliationService constructs a ReconciliationService backed by the
// given repository.
func NewReconciliationService(repo repository.NodeRepository) *ReconciliationService {
	return &ReconciliationService{repo: repo}
}

// Reconcile loads a node, compares its desired status to its actual status and
// returns the comparison result. It returns repository.ErrNotFound if the node
// does not exist.
func (s *ReconciliationService) Reconcile(ctx context.Context, id string) (*ReconciliationResult, error) {
	node, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	result := ReconciliationResult{
		NodeID:       node.ID,
		DesiredState: node.DesiredStatus,
		ActualState:  node.Status,
	}

	if node.Status == node.DesiredStatus {
		result.Result = ResultInSync
	} else {
		result.Result = ResultDriftDetected
	}

	return &result, nil
}
