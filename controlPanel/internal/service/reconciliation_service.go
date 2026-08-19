package service

import (
	"context"
	"log"
	"time"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
	"github.com/acegobugs-cpu/AetherGrid/internal/reconcile"
	"github.com/acegobugs-cpu/AetherGrid/internal/repository"
)

// ReconciliationService is the application-level facade over the reconciliation
// engine. Handlers talk to it; it owns the engine lifecycle.
type ReconciliationService struct {
	engine  *reconcile.Reconciler
	repo    repository.NodeRepository
	history repository.ReconciliationHistoryRepository
}

// NewReconciliationService builds the reconciliation engine and its facade.
// The engine is not started; call Start before serving requests.
func NewReconciliationService(
	cfg reconcile.Config,
	repo repository.NodeRepository,
	history repository.ReconciliationHistoryRepository,
	commands reconcile.CommandDispatcher,
	logger *log.Logger,
) *ReconciliationService {
	observer := reconcile.NewRepositoryObserver(repo, cfg.HeartbeatTimeout, nil)
	planner := reconcile.NewReconciliationPlanner()
	executor := reconcile.NewReconciliationExecutor(commands)

	engine := reconcile.NewReconciler(observer, planner, executor, repo, history.Create, cfg, logger, nil, nil)

	return &ReconciliationService{
		engine:  engine,
		repo:    repo,
		history: history,
	}
}

// Start launches the engine's workers and periodic sweep.
func (s *ReconciliationService) Start() {
	s.engine.Start()
}

// Stop shuts the engine down gracefully, waiting for in-flight cycles.
func (s *ReconciliationService) Stop() {
	s.engine.Stop()
}

// Notify triggers an immediate reconciliation for the node.
func (s *ReconciliationService) Notify(nodeID string) {
	s.engine.Notify(nodeID)
}

// Reconcile runs one full reconciliation cycle synchronously and returns the
// structured result. It is used by the manual POST /nodes/{id}/reconcile
// endpoint and returns repository.ErrNotFound when the node does not exist.
func (s *ReconciliationService) Reconcile(ctx context.Context, id string) (*domain.ReconciliationResult, error) {
	return s.engine.ReconcileNode(ctx, id)
}

// ReconciliationState returns the current reconciliation metadata of a node.
func (s *ReconciliationService) ReconciliationState(ctx context.Context, id string) (*domain.Node, error) {
	return s.repo.GetByID(ctx, id)
}

// History returns the most recent reconciliation events for a node, newest
// first.
func (s *ReconciliationService) History(ctx context.Context, id string, limit int) ([]*domain.ReconciliationEvent, error) {
	return s.history.ListByNode(ctx, id, limit)
}

// ControllerStatus describes the state of the reconciliation engine.
type ControllerStatus struct {
	Running            bool   `json:"running"`
	Workers            int    `json:"workers"`
	PendingWork        int    `json:"pending_work"`
	NodesReconciled    int64  `json:"nodes_reconciled"`
	CyclesInSync       int64  `json:"cycles_in_sync"`
	CyclesDrifted      int64  `json:"cycles_drifted"`
	CyclesReconciled   int64  `json:"cycles_reconciled"`
	CyclesFailed       int64  `json:"cycles_failed"`
	CommandsDispatched int64  `json:"commands_dispatched"`
	LastReconciliation string `json:"last_reconciliation,omitempty"`
}

// Status returns the engine's operational state and counters.
func (s *ReconciliationService) Status() ControllerStatus {
	metrics := s.engine.Metrics()
	last := metrics.LastReconciliation()
	status := ControllerStatus{
		Running:            true,
		Workers:            s.engine.Workers(),
		PendingWork:        s.engine.PendingWork(),
		NodesReconciled:    metrics.NodesReconciled.Load(),
		CyclesInSync:       metrics.CyclesInSync.Load(),
		CyclesDrifted:      metrics.CyclesDrifted.Load(),
		CyclesReconciled:   metrics.CyclesReconciled.Load(),
		CyclesFailed:       metrics.CyclesFailed.Load(),
		CommandsDispatched: metrics.CommandsDispatched.Load(),
	}
	if !last.IsZero() {
		status.LastReconciliation = last.UTC().Format(time.RFC3339Nano)
	}
	return status
}
