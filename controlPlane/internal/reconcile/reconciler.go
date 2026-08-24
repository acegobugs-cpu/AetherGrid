package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
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
	// FailureConfirmMultiplier is how many HeartbeatTimeout periods of
	// continued silence are required to confirm a failure. A node is suspect
	// after one period and failed after N periods. Defaults to 3.
	FailureConfirmMultiplier int
	// RecoveryPolicy configures autonomous recovery behavior.
	RecoveryPolicy RecoveryPolicy
}

// RecoveryPolicy configures the autonomous recovery behavior for edge nodes.
// The enablement flags are pointers so an explicitly disabled policy can be
// distinguished from an unset one; zero values mean worker recovery enabled
// and control-plane recovery disabled (the conservative Phase 9 defaults).
type RecoveryPolicy struct {
	// WorkerAutomaticRecovery enables automatic recovery for failed workers.
	WorkerAutomaticRecovery *bool `json:"worker_automatic_recovery,omitempty"`
	// ControlPlaneAutomaticRecovery enables automatic recovery for
	// control-plane nodes. Disabled by default due to HA/quorum complexity.
	ControlPlaneAutomaticRecovery *bool `json:"control_plane_automatic_recovery,omitempty"`
	// MaxRecoveryAttempts is the maximum number of recovery attempts before
	// the circuit breaker trips and blocks further recovery.
	MaxRecoveryAttempts int `json:"max_recovery_attempts"`
	// RecoveryBackoff is the backoff strategy between recovery attempts.
	RecoveryBackoff RecoveryBackoff `json:"recovery_backoff"`
	// MaxConcurrentRecoveries limits how many recovery operations can run
	// concurrently across the fleet.
	MaxConcurrentRecoveries int `json:"max_concurrent_recoveries"`
	// RecoveryCooldown is the minimum time before a previously-recovered node
	// can be considered for replacement again.
	RecoveryCooldown time.Duration `json:"recovery_cooldown"`
	// MaxReplacementsPerCluster is a hard safety limit (Phase 9 #64): when
	// this many members of one cluster are failed/blocked, further
	// replacements in that cluster are blocked. Zero disables the limit.
	MaxReplacementsPerCluster int `json:"max_replacements_per_cluster"`
}

// WorkerEnabled reports whether automatic worker recovery is permitted.
func (p RecoveryPolicy) WorkerEnabled() bool {
	return p.WorkerAutomaticRecovery == nil || *p.WorkerAutomaticRecovery
}

// ControlPlaneEnabled reports whether automatic control-plane recovery is
// permitted.
func (p RecoveryPolicy) ControlPlaneEnabled() bool {
	return p.ControlPlaneAutomaticRecovery != nil && *p.ControlPlaneAutomaticRecovery
}

// BoolPtr returns a pointer to b, a convenience for policy literals.
func BoolPtr(b bool) *bool { return &b }

// RecoveryBackoff configures the retry backoff strategy.
type RecoveryBackoff struct {
	// BaseDelay is the initial delay between retry attempts.
	BaseDelay time.Duration `json:"base_delay"`
	// MaxDelay is the maximum delay cap for exponential backoff.
	MaxDelay time.Duration `json:"max_delay"`
	// JitterEnabled enables random jitter to prevent synchronized retries.
	JitterEnabled bool `json:"jitter_enabled"`
}

// defaultBaseBackoff is the exponential backoff base delay.
const defaultBaseBackoff = 1 * time.Second

// DefaultRecoveryPolicy returns the conservative default recovery policy
// (Phase 9 #103): worker recovery enabled, control-plane recovery disabled,
// bounded attempts and concurrency.
func DefaultRecoveryPolicy() RecoveryPolicy {
	return RecoveryPolicy{
		WorkerAutomaticRecovery:       BoolPtr(true),
		ControlPlaneAutomaticRecovery: BoolPtr(false),
		MaxRecoveryAttempts:           3,
		RecoveryBackoff: RecoveryBackoff{
			BaseDelay:     defaultBaseBackoff,
			MaxDelay:      defaultBaseBackoff * 8,
			JitterEnabled: true,
		},
		MaxConcurrentRecoveries: 2,
		RecoveryCooldown:        30 * time.Minute,
	}
}

// Reconciler is the controller loop. It combines an observer, a planner and an
// executor with a periodic sweep, an event-driven notification channel and a
// bounded worker pool that serializes work per node.
type Reconciler struct {
	observer StateObserver
	planner  Planner
	executor Executor
	nodes    NodeRepository
	clusters ClusterInspector
	history  HistoryWriter
	cfg      Config
	logger   *log.Logger

	queue       *workQueue
	locks       *nodeLocks
	metrics     *Metrics
	recoverySem *semaphore

	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
	rngMu sync.Mutex
	rng   *rand.Rand

	wg      sync.WaitGroup
	cancel  context.CancelFunc
	started atomic.Bool
}

// ClusterInspector is the optional view of cluster membership the recovery
// preconditions need (Phase 9 #70). It is satisfied by the existing cluster
// service; a nil inspector skips cluster-level checks.
type ClusterInspector interface {
	// ForNode returns the AETHER-GRID-managed cluster the node belongs to,
	// or an error when the node belongs to no managed cluster.
	ForNode(ctx context.Context, nodeID string) (*domain.Cluster, error)
	// HasConflictingOperation reports whether a bootstrap or removal operation
	// is currently in flight for the cluster.
	HasConflictingOperation(ctx context.Context, clusterID string) (bool, error)
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
	if cfg.FailureConfirmMultiplier <= 0 {
		cfg.FailureConfirmMultiplier = 3
	}
	if cfg.RecoveryPolicy.MaxRecoveryAttempts <= 0 {
		cfg.RecoveryPolicy.MaxRecoveryAttempts = 3
	}
	if cfg.RecoveryPolicy.MaxConcurrentRecoveries <= 0 {
		cfg.RecoveryPolicy.MaxConcurrentRecoveries = 2
	}
	if cfg.RecoveryPolicy.RecoveryBackoff.BaseDelay <= 0 {
		cfg.RecoveryPolicy.RecoveryBackoff.BaseDelay = defaultBaseBackoff
	}
	if cfg.RecoveryPolicy.RecoveryBackoff.MaxDelay < cfg.RecoveryPolicy.RecoveryBackoff.BaseDelay {
		cfg.RecoveryPolicy.RecoveryBackoff.MaxDelay = cfg.RecoveryPolicy.RecoveryBackoff.BaseDelay * 8
	}

	reconciler := &Reconciler{
		observer:    observer,
		planner:     planner,
		executor:    executor,
		nodes:       nodes,
		history:     history,
		cfg:         cfg,
		logger:      logger,
		queue:       newWorkQueue(),
		locks:       newNodeLocks(),
		recoverySem: newSemaphore(cfg.RecoveryPolicy.MaxConcurrentRecoveries),
		rng:         rand.New(rand.NewSource(time.Now().UnixNano())),
		now:         now,
		sleep:       sleep,
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

// SetClusterInspector wires the optional cluster-membership view used by the
// recovery preconditions.
func (r *Reconciler) SetClusterInspector(clusters ClusterInspector) {
	r.clusters = clusters
}

// ResetRecovery clears the circuit-breaker state of a node so reconciliation
// may evaluate its failure again (Phase 9 #100). It deliberately does not
// trigger any recovery action itself; the next observe/plan cycle decides.
func (r *Reconciler) ResetRecovery(ctx context.Context, nodeID string) error {
	node, err := r.nodes.GetByID(ctx, nodeID)
	if err != nil {
		return err
	}
	node.RecoveryState = domain.RecoveryNotRequired
	node.RecoveryAttempts = 0
	node.FailureStreak = 0
	node.NextRetryAt = nil
	if err := r.nodes.UpdateReconciliation(ctx, node); err != nil {
		return err
	}
	r.audit(ctx, nodeID, "RECOVERY_RESET", "operator requested recovery re-evaluation")
	r.Notify(nodeID)
	return nil
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

// classifyFailure determines the category of a node failure based on the
// observed signals. The category guides the recovery strategy: an agent-level
// failure is remediated by restarting the agent while an infrastructure-level
// failure requires provisioning a replacement.
func (r *Reconciler) classifyFailure(node *domain.Node) domain.FailureClassification { // A stale heartbeat combined with an offline status means the machine or
	// its network path is gone: infrastructure level.
	if r.staleHeartbeat(node) {
		switch node.Status {
		case domain.StatusOffline, domain.StatusUnreachable:
			return domain.FailureInfrastructure
		case domain.StatusFailed:
			return domain.FailureInfrastructure
		default:
			return domain.FailureNetwork
		}
	}

	// Kubernetes reported problems while the machine itself reports in.
	if node.Kubernetes != nil {
		if !node.Kubernetes.Available || node.Kubernetes.Status == domain.KubernetesUnavailable {
			return domain.FailureKubernetes
		}
	}

	// No heartbeat ever received points at the agent bootstrap.
	if node.LastHeartbeat == nil {
		return domain.FailureAgent
	}

	return domain.FailureUnknown
}

// staleHeartbeat reports whether the node's last heartbeat is older than the
// configured timeout. Nodes that never reported keep their stored status.
func (r *Reconciler) staleHeartbeat(node *domain.Node) bool {
	if node.LastHeartbeat == nil {
		return false
	}
	return r.now().UTC().Sub(*node.LastHeartbeat) > r.cfg.HeartbeatTimeout
}

// failureConfirmed reports whether enough evidence exists to treat a node
// failure as permanent (Phase 9 #13). Evidence is:
//
//   - an explicit terminal status reported by the agent or provider, or
//   - heartbeat silence sustained for FailureConfirmMultiplier heartbeat
//     periods.
func (r *Reconciler) failureConfirmed(node *domain.Node) bool {
	switch node.Status {
	case domain.StatusFailed, domain.StatusUnreachable, domain.StatusRemoved:
		return true
	}

	if r.staleHeartbeat(node) {
		silent := r.now().UTC().Sub(*node.LastHeartbeat)
		threshold := r.cfg.HeartbeatTimeout * time.Duration(r.cfg.FailureConfirmMultiplier)
		return silent >= threshold
	}
	return false
}

// recoveryBlocked reports whether the recovery policy forbids automatic
// recovery for this node. This implements the Phase 9 safety rules:
//
//   - control-plane nodes are never automatically replaced,
//   - nodes whose circuit breaker tripped stay blocked until reset,
//   - nodes inside their post-recovery cooldown are left alone,
//   - flapping nodes are not repeatedly replaced.
func (r *Reconciler) recoveryBlocked(node *domain.Node, now time.Time) bool {
	policy := r.cfg.RecoveryPolicy

	switch node.Role {
	case domain.RoleControlPlane:
		if !policy.ControlPlaneEnabled() {
			return true
		}
	default:
		// Workers and unroled nodes follow the worker policy.
		if !policy.WorkerEnabled() {
			return true
		}
	}

	// Circuit breaker: too many failed recovery attempts.
	maxAttempts := policy.MaxRecoveryAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	if node.RecoveryAttempts >= maxAttempts {
		return true
	}

	// Explicitly blocked state persists until an operator resets it.
	if node.RecoveryState == domain.RecoveryBlocked {
		return true
	}

	// Post-recovery cooldown prevents oscillation right after a recovery.
	if node.LastRecoveryAt != nil &&
		node.RecoveryState == domain.RecoveryRecovered &&
		now.Sub(*node.LastRecoveryAt) < policy.RecoveryCooldown {
		return true
	}

	// Flapping protection: repeated failures without stable Ready periods.
	if r.isFlapping(node) {
		return true
	}

	return false
}

// flappingThreshold is how many confirmed failures within the cooldown window
// mark a node as flapping.
const flappingThreshold = 3

// preconditionsFailed enforces the destructive-recovery preconditions of
// Phase 9 #70. It returns "" when recovery may proceed or a human-readable
// reason when it must be blocked. Cluster-level checks are skipped when no
// cluster inspector is wired (unit-test engines).
func (r *Reconciler) preconditionsFailed(ctx context.Context, node *domain.Node) string {
	if r.clusters == nil {
		return ""
	}
	cluster, err := r.clusters.ForNode(ctx, node.ID)
	if err != nil || cluster == nil {
		return "node does not belong to a managed cluster"
	}
	conflicting, err := r.clusters.HasConflictingOperation(ctx, cluster.ID)
	if err != nil {
		return ""
	}
	if conflicting {
		return "conflicting cluster operation in progress"
	}
	if r.cfg.RecoveryPolicy.MaxReplacementsPerCluster > 0 &&
		node.RecoveryAttempts >= 1 &&
		r.replacementsInCluster(ctx, cluster) >= r.cfg.RecoveryPolicy.MaxReplacementsPerCluster {
		return "cluster replacement limit reached"
	}
	return ""
}

// replacementsInCluster counts nodes of the cluster that are currently mid
// replacement or already exhausted their recoveries.
func (r *Reconciler) replacementsInCluster(ctx context.Context, cluster *domain.Cluster) int {
	members := make(map[string]struct{}, len(cluster.Spec.WorkerNodes)+1)
	members[cluster.Spec.ControlPlaneNode] = struct{}{}
	for _, id := range cluster.Spec.WorkerNodes {
		members[id] = struct{}{}
	}
	nodes, err := r.nodes.GetAll(ctx)
	if err != nil {
		return 0
	}
	count := 0
	for _, n := range nodes {
		if _, member := members[n.ID]; !member {
			continue
		}
		if n.RecoveryState == domain.RecoveryFailed ||
			n.RecoveryState == domain.RecoveryBlocked ||
			n.Status == domain.StatusFailed {
			count++
		}
	}
	return count
}

// isFlapping reports whether the node has failed repeatedly without ever
// completing a successful recovery cycle, which indicates an unstable node
// rather than a transient fault.
func (r *Reconciler) isFlapping(node *domain.Node) bool {
	if node.FailureStreak < flappingThreshold {
		return false
	}
	// The streak only counts exhausted recoveries; a node that never entered
	// recovery cannot be flapping.
	if node.LastRecoveryAt == nil {
		return false
	}
	// A successful reconciliation newer than the last recovery attempt proves
	// the node stabilized again.
	if node.LastSuccessfulReconciliation != nil &&
		node.LastSuccessfulReconciliation.After(*node.LastRecoveryAt) {
		return false
	}
	return true
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

	// Reset recovery state on successful reconciliation.
	if node.RecoveryState == domain.RecoveryRecovering ||
		node.RecoveryState == domain.RecoveryVerification {
		node.RecoveryState = domain.RecoveryRecovered
		node.RecoveryFailure = ""
		node.LastRecoveryAt = &now
		node.RecoveryAttempts = 0
		r.metrics.recordRecoverySuccess()
	}
	if result.RecoveryState != "" {
		node.RecoveryState = result.RecoveryState
	}

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

	// Persist recovery state if provided.
	if result.RecoveryState != "" {
		node.RecoveryState = result.RecoveryState
	}
	if result.RecoveryAttempt > 0 {
		node.RecoveryAttempts = result.RecoveryAttempt
		node.LastRecoveryAt = &now
	}
	if result.FailureClass != "" {
		node.RecoveryFailure = string(result.FailureClass)
	}

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

		// A suspected node that started reporting again healed on its own:
		// transient failures must never trigger replacement (Phase 9 #88).
		if node.RecoveryState == domain.RecoverySuspected {
			result.Result = domain.ReconciliationReconciled
			result.RecoveryState = domain.RecoveryRecovered
			r.metrics.recordSuspicionCleared()
			r.metrics.recordCycle(now, string(result.Result))
			r.recordSuccess(ctx, node, result, now)
			result.CompletedAt = r.now().UTC()
			r.writeHistory(ctx, result)
			return result, nil
		}

		result.Result = domain.ReconciliationInSync
		result.Attempt = 0
		result.RecoveryState = domain.RecoveryNotRequired
		r.metrics.recordCycle(now, string(result.Result))
		r.recordSuccess(ctx, node, result, now)
		result.CompletedAt = r.now().UTC()
		return result, nil
	}

	// Drift with no corrective action available (transitional status or
	// detect-only mode).
	if plan.Action == "" || r.cfg.MaxRetries <= 0 {
		// Heartbeat silence alone is never proof of failure: mark the node
		// suspect and wait for the confirmation threshold (Phase 9 #24).
		if r.staleHeartbeat(node) {
			result.RecoveryState = domain.RecoverySuspected
			result.FailureClass = r.classifyFailure(node)
			if node.RecoveryState != domain.RecoverySuspected {
				r.metrics.recordSuspicion()
			}
		}
		result.Result = domain.ReconciliationDriftDetected
		r.metrics.recordCycle(now, string(result.Result))
		r.persistState(ctx, node, result, now, nil)
		result.CompletedAt = r.now().UTC()
		r.writeHistory(ctx, result)
		return result, nil
	}

	// ---- Phase 9 recovery gate -------------------------------------------
	// Failure must be confirmed by sustained evidence before any corrective
	// action runs. One missed heartbeat only makes a node suspect.
	failureClass := r.classifyFailure(node)
	confirmed := r.failureConfirmed(node)

	if !confirmed && plan.Action == ActionRecoverNode {
		result.Result = domain.ReconciliationDriftDetected
		result.RecoveryState = domain.RecoverySuspected
		result.FailureClass = failureClass
		if node.RecoveryState != domain.RecoverySuspected {
			r.metrics.recordSuspicion()
			r.audit(ctx, nodeID, AuditNodeFailureDetected, fmt.Sprintf(
				"class=%s confirmation_threshold=%dx", failureClass, r.cfg.FailureConfirmMultiplier))
			r.logger.Printf("node marked unreachable node=%s class=%s confirmation_threshold=%dx",
				nodeID, failureClass, r.cfg.FailureConfirmMultiplier)
		}
		r.persistState(ctx, node, result, now, nil)
		result.CompletedAt = r.now().UTC()
		r.writeHistory(ctx, result)
		return result, nil
	}

	// First crossing of the confirmation threshold is auditable.
	if confirmed && node.RecoveryState == domain.RecoverySuspected {
		r.audit(ctx, nodeID, AuditNodeFailureConfirmed, "class="+string(failureClass))
	}

	// Recovery preconditions (Phase 9 #70): cluster membership, ownership and
	// absence of conflicting operations are verified before anything runs.
	if blockReason := r.preconditionsFailed(ctx, node); blockReason != "" {
		result.Result = domain.ReconciliationDriftDetected
		result.CircuitBreaker = true
		result.RecoveryState = domain.RecoveryBlocked
		result.FailureClass = failureClass
		result.Error = blockReason
		r.metrics.recordRecoveryBlocked()
		r.audit(ctx, nodeID, AuditRecoveryBlocked, blockReason)
		r.persistState(ctx, node, result, now, nil)
		result.CompletedAt = r.now().UTC()
		return result, nil
	}

	// Policy / circuit-breaker gate: blocked recoveries are recorded, never
	// executed silently.
	if r.recoveryBlocked(node, now) {
		wasBlocked := node.RecoveryState == domain.RecoveryBlocked
		result.Result = domain.ReconciliationDriftDetected
		result.CircuitBreaker = true
		result.RecoveryState = domain.RecoveryBlocked
		result.FailureClass = failureClass
		result.Error = "recovery blocked by policy or circuit breaker"
		if !wasBlocked {
			r.metrics.recordRecoveryBlocked()
			r.audit(ctx, nodeID, AuditRecoveryBlocked, fmt.Sprintf(
				"role=%s attempts=%d class=%s", node.Role, node.RecoveryAttempts, failureClass))
			r.logger.Printf("recovery blocked node=%s role=%s attempts=%d class=%s",
				nodeID, node.Role, node.RecoveryAttempts, failureClass)
		}
		r.persistState(ctx, node, result, now, nil)
		result.CompletedAt = r.now().UTC()
		r.writeHistory(ctx, result)
		return result, nil
	}

	// Progressive escalation (Phase 9 #42): the least destructive action is
	// attempted first. Agent/network/kubernetes level failures are remediated
	// by restarting the agent. Only an infrastructure-classified failure on a
	// worker that has exhausted agent-level retries escalates to replacement.
	escalated := plan.Action
	if failureClass == domain.FailureInfrastructure &&
		node.Role != domain.RoleControlPlane &&
		node.ReconciliationAttempts > 0 &&
		node.ReconciliationAttempts < r.cfg.MaxRetries {
		escalated = ActionProvisionReplacement
	}
	if escalated != plan.Action {
		plan.Action = escalated
	}
	// -----------------------------------------------------------------------

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
		result.RecoveryState = domain.RecoveryRecovering
		result.FailureClass = failureClass
		result.RecoveryAttempt = node.RecoveryAttempts + 1

		if result.RecoveryAttempt == 1 {
			r.metrics.recordRecoveryStarted()
			r.audit(ctx, nodeID, AuditRecoveryStarted, fmt.Sprintf(
				"role=%s class=%s action=%s", node.Role, failureClass, plan.Action))
			r.logger.Printf("recovery started node=%s role=%s class=%s action=%s",
				nodeID, node.Role, failureClass, plan.Action)
		}
		r.metrics.recordRecoveryAttempt()

		// Bounded recovery concurrency (Phase 9 #34): wait for a slot before
		// dispatching so simultaneous failures cannot stampede the provider.
		if !r.recoverySem.acquire(ctx) {
			return nil, ctx.Err()
		}

		nextRetry := now.Add(backoffJittered(
			backoff(result.RecoveryAttempt,
				r.cfg.RecoveryPolicy.RecoveryBackoff.BaseDelay,
				r.cfg.RecoveryPolicy.RecoveryBackoff.MaxDelay),
			r.rng))
		node.NextRetryAt = &nextRetry
		result.NextRetryAt = &nextRetry
		r.persistState(ctx, node, result, now, &deadline)

		err := r.executor.Execute(ctx, plan)
		r.recoverySem.release()
		if err == nil {
			r.metrics.recordCommand()
			if plan.Action == ActionProvisionReplacement {
				r.audit(ctx, nodeID, AuditReplacementProvisioned, "action="+plan.Action)
			}

			// Re-observe to verify convergence. Convergence is only trusted
			// once the next cycle re-verifies; until then the node stays in
			// the Verification recovery state.
			converged, convErr := r.converged(ctx, nodeID)
			if convErr != nil {
				return nil, convErr
			}
			if converged {
				result.Result = domain.ReconciliationReconciled
				result.Attempt = attempt
				result.RecoveryState = domain.RecoveryRecovered
				r.metrics.recordCycle(now, string(result.Result))
				r.recordSuccess(ctx, node, result, now)
				r.audit(ctx, nodeID, AuditNodeRejoined, "action="+plan.Action)
				r.audit(ctx, nodeID, AuditRecoveryCompleted, "result=RECONCILED")
				r.logger.Printf("reconciliation completed node=%s action=%s attempt=%d result=%s",
					nodeID, plan.Action, attempt, result.Result)
			} else {
				result.Result = domain.ReconciliationReconciling
				result.RecoveryState = domain.RecoveryVerification
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
			result.RecoveryState = domain.RecoveryFailed
			node.FailureStreak++
			r.metrics.recordRecoveryFailure()
			r.metrics.recordCycle(now, string(result.Result))
			r.persistState(ctx, node, result, now, nil)
			result.CompletedAt = r.now().UTC()
			r.writeHistory(ctx, result)
			r.audit(ctx, nodeID, AuditRecoveryAttemptFailed, fmt.Sprintf(
				"action=%s attempt=%d error=%q", plan.Action, attempt, result.Error))
			r.logger.Printf("reconciliation failed node=%s action=%s attempt=%d error=%q",
				nodeID, plan.Action, attempt, result.Error)
			return result, nil
		}

		r.audit(ctx, nodeID, AuditRecoveryAttemptFailed, fmt.Sprintf(
			"action=%s attempt=%d retry_scheduled", plan.Action, attempt))
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
	result.RecoveryState = domain.RecoveryFailed
	node.FailureStreak++
	r.metrics.recordRecoveryFailure()
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
