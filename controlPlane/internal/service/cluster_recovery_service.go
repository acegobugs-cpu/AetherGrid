package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"AetherGrid/controlPlane/internal/domain"
	"AetherGrid/controlPlane/internal/reconcile"
	"AetherGrid/controlPlane/internal/repository"
)

// ClusterInspectorAdapter adapts the existing cluster and operation
// repositories onto the reconcile.ClusterInspector preconditions interface
// (Phase 9 #70). Recovery reuses the Phase 8 per-cluster data; no second
// cluster abstraction is introduced.
type ClusterInspectorAdapter struct {
	clusters repository.ClusterRepository
	ops      repository.ClusterOperationRepository
	nodes    repository.NodeRepository
	logger   *log.Logger
}

// NewClusterInspectorAdapter builds the adapter.
func NewClusterInspectorAdapter(
	clusters repository.ClusterRepository,
	ops repository.ClusterOperationRepository,
	nodes repository.NodeRepository,
	logger *log.Logger,
) *ClusterInspectorAdapter {
	return &ClusterInspectorAdapter{clusters: clusters, ops: ops, nodes: nodes, logger: logger}
}

// ForNode returns the managed cluster whose spec references the node, either
// as control-plane or as worker. It returns an error when no managed cluster
// owns the node, which blocks destructive recovery of unmanaged machines.
func (a *ClusterInspectorAdapter) ForNode(ctx context.Context, nodeID string) (*domain.Cluster, error) {
	clusters, err := a.clusters.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing clusters for node %q: %w", nodeID, err)
	}
	for _, cluster := range clusters {
		if cluster.Spec.ControlPlaneNode == nodeID {
			return cluster, nil
		}
		for _, worker := range cluster.Spec.WorkerNodes {
			if worker == nodeID {
				return cluster, nil
			}
		}
	}
	return nil, fmt.Errorf("node %q does not belong to any managed cluster", nodeID)
}

// HasConflictingOperation reports whether a bootstrap or removal operation is
// currently PENDING or RUNNING for the cluster (Phase 9 #36).
func (a *ClusterInspectorAdapter) HasConflictingOperation(ctx context.Context, clusterID string) (bool, error) {
	operations, err := a.ops.ListOperationsByCluster(ctx, clusterID)
	if err != nil {
		return false, err
	}
	for _, op := range operations {
		if op.Status == domain.ClusterOpPending || op.Status == domain.ClusterOpRunning {
			return true, nil
		}
	}
	return false, nil
}

// Compile-time interface check.
var _ reconcile.ClusterInspector = (*ClusterInspectorAdapter)(nil)

// ClusterHealth summarizes the recovery-relevant health of one cluster.
type ClusterHealth struct {
	ClusterID      string                       `json:"cluster_id"`
	State          domain.ClusterLifecycleState `json:"state"`
	DesiredWorkers int                          `json:"desired_workers"`
	HealthyWorkers int                          `json:"healthy_workers"`
	Degraded       bool                         `json:"degraded"`
	Members        []NodeRecoveryStatus         `json:"members"`
}

// NodeRecoveryStatus is the per-member reconciliation and recovery view.
type NodeRecoveryStatus struct {
	NodeID               string                      `json:"node_id"`
	Name                 string                      `json:"name"`
	Status               domain.NodeStatus           `json:"status"`
	Role                 domain.ClusterRole          `json:"role"`
	LastHeartbeat        *time.Time                  `json:"last_heartbeat,omitempty"`
	ReconciliationStatus domain.ReconciliationStatus `json:"reconciliation_result,omitempty"`
	RecoveryState        domain.RecoveryState        `json:"recovery_state"`
	FailureClass         string                      `json:"failure_class,omitempty"`
	RecoveryAttempts     int                         `json:"recovery_attempts"`
	FailureStreak        int                         `json:"failure_streak"`
}

// ClusterHealth computes the aggregate health of the cluster from observed
// member state. A cluster is Degraded while at least one desired worker is
// not healthy; Ready only when every declared member is healthy.
func (s *ReconciliationService) ClusterHealth(ctx context.Context, id string) (*ClusterHealth, error) {
	cluster, err := s.clusters.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	memberIDs := append([]string{cluster.Spec.ControlPlaneNode}, cluster.Spec.WorkerNodes...)

	health := &ClusterHealth{
		ClusterID:      cluster.ID,
		State:          cluster.Status.State,
		DesiredWorkers: len(cluster.Spec.WorkerNodes),
	}
	healthyWorkers := 0

	for _, memberID := range memberIDs {
		node, err := s.repo.GetByID(ctx, memberID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				health.Members = append(health.Members, NodeRecoveryStatus{
					NodeID: memberID, Status: domain.StatusUnknown,
					RecoveryState: domain.RecoveryNotRequired,
				})
				continue
			}
			return nil, err
		}
		healthy := node.Status == domain.StatusReady &&
			node.LastHeartbeat != nil && !s.isStale(node)
		status := NodeRecoveryStatus{
			NodeID:               node.ID,
			Name:                 node.Name,
			Status:               node.Status,
			Role:                 node.Role,
			LastHeartbeat:        node.LastHeartbeat,
			ReconciliationStatus: node.LastReconciliationResult,
			RecoveryState:        node.RecoveryState,
			FailureClass:         node.RecoveryFailure,
			RecoveryAttempts:     node.RecoveryAttempts,
			FailureStreak:        node.FailureStreak,
		}
		if healthy && node.Role != domain.RoleControlPlane {
			healthyWorkers++
		}
		if node.RecoveryState == domain.RecoveryRecovering ||
			node.RecoveryState == domain.RecoveryVerification {
			health.State = domain.ClusterStateRecovering
		}
		health.Members = append(health.Members, status)
	}

	health.HealthyWorkers = healthyWorkers
	health.Degraded = healthyWorkers < health.DesiredWorkers ||
		health.State == domain.ClusterStateDegraded ||
		health.State == domain.ClusterStateRecovering
	return health, nil
}

// ClusterRecovery returns the recovery view of every cluster member.
func (s *ReconciliationService) ClusterRecovery(ctx context.Context, id string) ([]NodeRecoveryStatus, error) {
	health, err := s.ClusterHealth(ctx, id)
	if err != nil {
		return nil, err
	}
	return health.Members, nil
}

// ReconcileCluster runs one reconciliation cycle for every declared member.
// Manual triggering never bypasses policies, locks or confirmation thresholds;
// each member goes through the same gate an automatic cycle uses (Phase 9 #99).
func (s *ReconciliationService) ReconcileCluster(ctx context.Context, id string) ([]*domain.ReconciliationResult, error) {
	cluster, err := s.clusters.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	memberIDs := append([]string{cluster.Spec.ControlPlaneNode}, cluster.Spec.WorkerNodes...)
	results := make([]*domain.ReconciliationResult, 0, len(memberIDs))
	for _, memberID := range memberIDs {
		result, err := s.engine.ReconcileNode(ctx, memberID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				continue
			}
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

// ResetNodeRecovery clears the circuit breaker of one node so reconciliation
// evaluates its failure again. It does not execute any recovery (Phase 9 #100).
func (s *ReconciliationService) ResetNodeRecovery(ctx context.Context, nodeID string) error {
	return s.engine.ResetRecovery(ctx, nodeID)
}
