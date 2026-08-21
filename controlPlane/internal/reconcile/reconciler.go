package reconcile

import (
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"AetherGrid/controlPlane/internal/domain"
)

// Config tunes the reconciliation engine.
type Config struct {
	// Interval is how often the periodic sweep runs.
	Interval time.Duration
	// Workers is the size of the bounded worker pool.
	Workers int
	// HeartbeatTimeout is the staleness threshold that makes a node OFFLINE.
	HeartbeatTimeout time.Duration
	// MaxRetries is the maximum number of execution attempts per drift
	// resolution. A value <= 0 enables detect-only mode: drift is reported but
	// never acted on.
	MaxRetries int
	// MaxBackoff is the upper bound for exponential backoff between retries.
	MaxBackoff time.Duration
	// RecoveryTimeout is how long a dispatched recovery is allowed to converge
	// before it is considered timed out.
	RecoveryTimeout time.Duration
}

// defaultBaseBackoff is the exponential backoff base delay.
const defaultBaseBackoff = 1 * time.Second

// Reconciler is the controller loop. It combines an observer, a planner and an
// executor with a periodic sweep, an event-driven notification channel and a
// bounded worker pool that serializes work per node.
type Reconciler struct {
	observer StateObserver
	planner  Planner
	executor Executor
	nodes    NodeRepository
	history  HistoryWriter
	cfg      Config
	logger   *log.Logger

	queue   *workQueue
	locks   *nodeLocks
	metrics *Metrics

	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error

	wg      sync.WaitGroup
	cancel  context.CancelFunc
	started atomic.Bool
}

// NewReconciler constructs a reconciliation engine. The optional history hook
// persists reconciliation events; the optional now/sleep functions are
// injectable for tests.
func NewReconciler(
	observer StateObserver,
	planner Planner,
	executor Executor,
	nodes NodeRepository,
	history HistoryWriter,
	cfg Config,
	logger *log.Logger,
	now func() time.Time,
	sleep func(ctx context.Context, d time.Duration) error,
) *Reconciler {
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}

	reconciler := &Reconciler{
		observer: observer,
		planner:  planner,
		executor: executor,
		nodes:    nodes,
		history:  history,
		cfg:      cfg,
		logger:   logger,
		queue:    newWorkQueue(),
		locks:    newNodeLocks(),
		now:      now,
		sleep:    sleep,
	}
	reconciler.metrics = newMetrics(reconciler.queue.Len)
	return reconciler
}

// Start launches the worker pool and the periodic sweep loop. It is
// idempotent.
func (r *Reconciler) Start() {
	if !r.started.CompareAndSwap(false, true) {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel

	for i := 0; i < r.cfg.Workers; i++ {
		r.wg.Add(1)
		go r.worker(ctx)
	}
	r.wg.Add(1)
	go r.sweepLoop(ctx)

	r.logger.Printf("reconciliation engine started: interval=%s workers=%d heartbeat_timeout=%s max_retries=%d",
		r.cfg.Interval, r.cfg.Workers, r.cfg.HeartbeatTimeout, r.cfg.MaxRetries)
}

// Stop cancels the engine, stops the periodic loop and waits for in-flight
// cycles to finish. It is idempotent.
func (r *Reconciler) Stop() {
	if !r.started.CompareAndSwap(true, false) {
		return
	}
	r.cancel()
	r.wg.Wait()
	r.logger.Printf("reconciliation engine stopped")
}

// Notify schedules an immediate reconciliation for a node, coalescing
// duplicate notifications for the same node.
func (r *Reconciler) Notify(nodeID string) {
	r.queue.Enqueue(nodeID)
}

// ReconcileNode runs one full reconciliation cycle for a node and returns its
// result. It is safe for concurrent use; per-node serialization guarantees a
// node is never reconciled by two workers at once. It returns the repository
// error when the node does not exist.
func (r *Reconciler) ReconcileNode(ctx context.Context, nodeID string) (*domain.ReconciliationResult, error) {
	r.locks.acquire(nodeID)
	defer r.locks.release(nodeID)
	return r.reconcileNode(ctx, nodeID)
}

// Metrics returns the engine's instrumentation counters.
func (r *Reconciler) Metrics() *Metrics {
	return r.metrics
}

// PendingWork reports how many nodes await reconciliation.
func (r *Reconciler) PendingWork() int {
	return r.queue.Len()
}

// Workers returns the configured worker-pool size.
func (r *Reconciler) Workers() int {
	return r.cfg.Workers
}

// worker drains the work queue and reconciles each enqueued node.
func (r *Reconciler) worker(ctx context.Context) {
	defer r.wg.Done()
	for {
		nodeID := r.queue.Dequeue()
		if nodeID == "" {
			select {
			case <-ctx.Done():
				return
			case <-r.queue.notify:
				continue
			}
		}

		if _, err := r.ReconcileNode(ctx, nodeID); err != nil {
			if ctx.Err() != nil {
				return
			}
			r.logger.Printf("reconciliation failed node=%s error=%v", nodeID, err)
		}
	}
}

// sweepLoop periodically observes the fleet and enqueues nodes that need
// corrective action.
func (r *Reconciler) sweepLoop(ctx context.Context) {
	defer r.wg.Done()

	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sweep(ctx)
		}
	}
}

// sweep observes every node, plans for each and enqueues those that need
// action. The authoritative plan is computed again by the worker under the
// per-node lock; this pass only fills the queue.
func (r *Reconciler) sweep(ctx context.Context) {
	strategies, err := r.observer.Observe(ctx)
	if err != nil {
		r.logger.Printf("observation failed: %v", err)
		return
	}

	for _, strategy := range strategies {
		plan, err := r.planner.Plan(ctx, strategy)
		if err != nil {
			r.logger.Printf("planning failed node=%s error=%v", strategy.NodeID, err)
			continue
		}
		if plan.NeedsAction() {
			r.queue.Enqueue(plan.NodeID)
		}
	}
}

// isRetryable reports whether a failed action may be retried.
func (r *Reconciler) isRetryable(err error) bool {
	var unsupported *UnsupportedActionError
	if errors.As(err, &unsupported) {
		return false
	}
	var retryable *RetryableError
	return errors.As(err, &retryable)
}

// recordSuccess persists the metadata and history for a converged node.
func (r *Reconciler) recordSuccess(ctx context.Context, node *domain.Node, result *domain.ReconciliationResult, now time.Time) {
	node.LastReconciliation = &now
	node.LastSuccessfulReconciliation = &now
	node.LastReconciliationResult = result.Result
	node.LastReconciliationAction = result.Action
	node.LastReconciliationError = ""
	node.LastReconciliationDeadline = nil
	node.ReconciliationAttempts = result.Attempt

	if err := r.nodes.UpdateReconciliation(ctx, node); err != nil {
		r.logger.Printf("persisting reconciliation success node=%s error=%v", node.ID, err)
	}
}

// persistState writes the reconciliation metadata for a node.
func (r *Reconciler) persistState(ctx context.Context, node *domain.Node, result *domain.ReconciliationResult, now time.Time, deadline *time.Time) {
	node.LastReconciliation = &now
	node.LastReconciliationResult = result.Result
	node.LastReconciliationAction = result.Action
	node.LastReconciliationError = result.Error
	node.LastReconciliationDeadline = deadline
	node.ReconciliationAttempts = result.Attempt

	if err := r.nodes.UpdateReconciliation(ctx, node); err != nil {
		r.logger.Printf("persisting reconciliation state node=%s result=%s error=%v", node.ID, result.Result, err)
	}
}

// writeHistory persists an operational history row, unless it is a steady-state
// IN_SYNC cycle (which would flood the table).
func (r *Reconciler) writeHistory(ctx context.Context, result *domain.ReconciliationResult) {
	if r.history == nil || result.Result == domain.ReconciliationInSync {
		return
	}
	event := &domain.ReconciliationEvent{
		NodeID:      result.NodeID,
		StartedAt:   result.StartedAt,
		CompletedAt: result.CompletedAt,
		Result:      result.Result,
		Action:      result.Action,
		Attempt:     result.Attempt,
		Error:       result.Error,
	}
	if err := r.history(ctx, event); err != nil {
		r.logger.Printf("persisting reconciliation history node=%s error=%v", result.NodeID, err)
	}
}

// reconcileNode implements one full observe -> compare -> plan -> execute ->
// observe cycle for a single node. The caller must hold the per-node lock.
func (r *Reconciler) reconcileNode(ctx context.Context, nodeID string) (*domain.ReconciliationResult, error) {
	node, err := r.nodes.GetByID(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	now := r.now().UTC()
	result := &domain.ReconciliationResult{
		NodeID:       nodeID,
		DesiredState: node.DesiredState(),
		ActualState:  node.ActualState(),
		Attempt:      node.ReconciliationAttempts,
		StartedAt:    now,
	}

	strategy, err := r.observer.Node(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	plan, err := r.planner.Plan(ctx, strategy)
	if err != nil {
		return nil, err
	}
	result.DesiredState = plan.Desired
	result.ActualState = plan.Actual
	result.Differences = plan.Differences

	// Converged: the node matches its desired state.
	if len(plan.Differences) == 0 {
		if node.LastReconciliationResult == domain.ReconciliationReconciling {
			// A previously dispatched recovery just converged.
			result.Result = domain.ReconciliationReconciled
			result.Action = node.LastReconciliationAction
			result.Attempt = node.ReconciliationAttempts
			r.metrics.recordCycle(now, string(result.Result))
			r.recordSuccess(ctx, node, result, now)
			result.CompletedAt = r.now().UTC()
			r.writeHistory(ctx, result)
			return result, nil
		}

		result.Result = domain.ReconciliationInSync
		result.Attempt = 0
		r.metrics.recordCycle(now, string(result.Result))
		r.recordSuccess(ctx, node, result, now)
		result.CompletedAt = r.now().UTC()
		return result, nil
	}

	// Drift with no corrective action available (transitional status or
	// detect-only mode).
	if plan.Action == "" || r.cfg.MaxRetries <= 0 {
		result.Result = domain.ReconciliationDriftDetected
		r.metrics.recordCycle(now, string(result.Result))
		r.persistState(ctx, node, result, now, nil)
		result.CompletedAt = r.now().UTC()
		r.writeHistory(ctx, result)
		return result, nil
	}

	// An earlier recovery is still in flight and has not timed out: do not
	// re-dispatch; report the ongoing progress.
	if node.LastReconciliationResult == domain.ReconciliationReconciling &&
		node.LastReconciliationDeadline != nil &&
		node.LastReconciliationDeadline.After(now) {
		result.Result = domain.ReconciliationReconciling
		result.Action = node.LastReconciliationAction
		result.Attempt = node.ReconciliationAttempts
		r.persistState(ctx, node, result, now, node.LastReconciliationDeadline)
		result.CompletedAt = r.now().UTC()
		return result, nil
	}

	// Execute the corrective action with exponential backoff.
	attempt := node.ReconciliationAttempts + 1
	for attempt <= r.cfg.MaxRetries {
		deadline := now.Add(r.cfg.RecoveryTimeout)

		result.Result = domain.ReconciliationReconciling
		result.Action = plan.Action
		result.Attempt = attempt
		result.Error = ""
		r.persistState(ctx, node, result, now, &deadline)

		err := r.executor.Execute(ctx, plan)
		if err == nil {
			r.metrics.recordCommand()

			// Re-observe to verify convergence.
			converged, convErr := r.converged(ctx, nodeID)
			if convErr != nil {
				return nil, convErr
			}
			if converged {
				result.Result = domain.ReconciliationReconciled
				result.Attempt = attempt
				r.metrics.recordCycle(now, string(result.Result))
				r.recordSuccess(ctx, node, result, now)
				r.logger.Printf("reconciliation completed node=%s action=%s attempt=%d result=%s",
					nodeID, plan.Action, attempt, result.Result)
			} else {
				result.Result = domain.ReconciliationReconciling
				r.metrics.recordCycle(now, string(result.Result))
				r.persistState(ctx, node, result, now, &deadline)
			}
			result.CompletedAt = r.now().UTC()
			r.writeHistory(ctx, result)
			return result, nil
		}

		// The action failed.
		result.Attempt = attempt
		result.Error = err.Error()
		result.Retryable = r.isRetryable(err)

		if !r.isRetryable(err) || attempt >= r.cfg.MaxRetries {
			result.Result = domain.ReconciliationFailed
			result.CompletedAt = r.now().UTC()
			r.metrics.recordCycle(now, string(result.Result))
			r.persistState(ctx, node, result, now, nil)
			r.writeHistory(ctx, result)
			r.logger.Printf("reconciliation failed node=%s action=%s attempt=%d error=%q",
				nodeID, plan.Action, attempt, result.Error)
			return result, nil
		}

		r.logger.Printf("reconciliation retry scheduled node=%s action=%s attempt=%d error=%q",
			nodeID, plan.Action, attempt, result.Error)
		r.persistState(ctx, node, result, now, &deadline)

		if err := r.sleep(ctx, backoff(attempt, defaultBaseBackoff, r.cfg.MaxBackoff)); err != nil {
			return nil, err
		}
		now = r.now().UTC()
		attempt++
	}

	result.Result = domain.ReconciliationFailed
	result.Attempt = attempt - 1
	result.Error = "maximum reconciliation attempts reached"
	result.CompletedAt = r.now().UTC()
	r.persistState(ctx, node, result, now, nil)
	r.writeHistory(ctx, result)
	return result, nil
}

// converged reports whether the node currently matches its desired state. It
// uses the planner so heartbeat staleness is taken into account: a node that
// has gone quiet is NOT converged even if its stored status matches.
func (r *Reconciler) converged(ctx context.Context, nodeID string) (bool, error) {
	strategy, err := r.observer.Node(ctx, nodeID)
	if err != nil {
		return false, err
	}
	plan, err := r.planner.Plan(ctx, strategy)
	if err != nil {
		return false, err
	}
	return len(plan.Differences) == 0, nil
}
