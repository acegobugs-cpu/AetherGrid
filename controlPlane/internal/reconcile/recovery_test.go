package reconcile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"testing"
	"time"

	"AetherGrid/controlPlane/internal/domain"
)

// Phase 9 recovery test configuration: heartbeat timeout 30s, confirmation
// after 3x silence (90s), matching the spec's example thresholds.
func recoveryTestConfig() Config {
	return Config{
		Interval:                 time.Second,
		Workers:                  2,
		HeartbeatTimeout:         30 * time.Second,
		MaxRetries:               3,
		MaxBackoff:               time.Second,
		RecoveryTimeout:          time.Minute,
		FailureConfirmMultiplier: 3,
	}
}

// suspectNode returns a node whose heartbeat silence is past the staleness
// threshold but below the failure-confirmation threshold.
func suspectNode(id string) *domain.Node {
	heartbeat := nowValue.Add(-60 * time.Second) // stale (>30s) but not confirmed (<90s)
	return &domain.Node{
		ID:            id,
		Name:          id,
		Status:        domain.StatusReady,
		DesiredStatus: domain.StatusReady,
		LastHeartbeat: &heartbeat,
		CreatedAt:     nowValue,
		UpdatedAt:     nowValue,
	}
}

// Test 1 — Heartbeat silence only marks a node SUSPECTED before the
// confirmation threshold; no corrective action is dispatched (spec #24/#88).
func TestRecoverySuspectedBeforeConfirmation(t *testing.T) {
	repo := newFakeNodeRepo()
	repo.add(suspectNode("worker-01"))
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, _ := testEngine(t, repo, dispatcher, recoveryTestConfig())

	result, err := engine.ReconcileNode(context.Background(), "worker-01")
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.Result != domain.ReconciliationDriftDetected {
		t.Errorf("expected DRIFT_DETECTED while unconfirmed, got %s", result.Result)
	}
	if result.RecoveryState != domain.RecoverySuspected {
		t.Errorf("expected SUSPECTED recovery state, got %s", result.RecoveryState)
	}
	if dispatcher.callCount() != 0 {
		t.Errorf("no command may be dispatched before confirmation, got %d", dispatcher.callCount())
	}
}

// Test 2 — A transient failure that heals before the confirmation threshold
// returns the node to Ready without any replacement or dispatch.
func TestTransientFailureHealsWithoutReplacement(t *testing.T) {
	repo := newFakeNodeRepo()
	repo.add(suspectNode("worker-01"))
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, _ := testEngine(t, repo, dispatcher, recoveryTestConfig())

	if _, err := engine.ReconcileNode(context.Background(), "worker-01"); err != nil {
		t.Fatalf("first cycle failed: %v", err)
	}

	// The agent comes back and reports READY with a fresh heartbeat.
	repo.setActual("worker-01", domain.StatusReady, nowValue)

	result, err := engine.ReconcileNode(context.Background(), "worker-01")
	if err != nil {
		t.Fatalf("second cycle failed: %v", err)
	}
	if result.Result != domain.ReconciliationInSync && result.Result != domain.ReconciliationReconciled {
		t.Errorf("expected healed node, got %s", result.Result)
	}
	if dispatcher.callCount() != 0 {
		t.Errorf("transient failure must not dispatch recovery, got %d dispatches", dispatcher.callCount())
	}
}

// Test 3 — Sustained silence beyond the confirmation threshold confirms the
// failure and triggers recovery for a worker.
func TestConfirmedWorkerFailureTriggersRecovery(t *testing.T) {
	repo := newFakeNodeRepo()
	node := offlineNode("worker-01", domain.StatusReady) // 24h silence >> 90s
	node.Role = domain.RoleWorker
	repo.add(node)
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, history := testEngine(t, repo, dispatcher, recoveryTestConfig())

	result, err := engine.ReconcileNode(context.Background(), "worker-01")
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.Result != domain.ReconciliationReconciled && result.Result != domain.ReconciliationReconciling {
		t.Errorf("expected recovery to run, got %s", result.Result)
	}
	if dispatcher.callCount() == 0 {
		t.Error("confirmed worker failure must dispatch a recovery command")
	}
	if result.FailureClass == "" {
		t.Error("expected a failure classification on the result")
	}
	if history.count() == 0 {
		t.Error("recovery must be auditable through history events")
	}
}

// Test 4 — Control-plane nodes are never automatically recovered by default
// (spec #27/#103).
func TestControlPlaneAutomaticRecoveryDisabled(t *testing.T) {
	repo := newFakeNodeRepo()
	node := offlineNode("cp-01", domain.StatusReady)
	node.Role = domain.RoleControlPlane
	repo.add(node)
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, _ := testEngine(t, repo, dispatcher, recoveryTestConfig())

	result, err := engine.ReconcileNode(context.Background(), "cp-01")
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if !result.CircuitBreaker || result.RecoveryState != domain.RecoveryBlocked {
		t.Errorf("control-plane recovery must be blocked, got state=%s breaker=%v",
			result.RecoveryState, result.CircuitBreaker)
	}
	if dispatcher.callCount() != 0 {
		t.Errorf("no automatic control-plane recovery is allowed, got %d dispatches", dispatcher.callCount())
	}
}

// Test 5 — Disabling the worker policy blocks worker recovery too.
func TestWorkerPolicyDisabledBlocksRecovery(t *testing.T) {
	repo := newFakeNodeRepo()
	node := offlineNode("worker-01", domain.StatusReady)
	node.Role = domain.RoleWorker
	repo.add(node)
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}

	cfg := recoveryTestConfig()
	cfg.RecoveryPolicy = RecoveryPolicy{WorkerAutomaticRecovery: BoolPtr(false)}
	engine, _ := testEngine(t, repo, dispatcher, cfg)

	result, err := engine.ReconcileNode(context.Background(), "worker-01")
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if !result.CircuitBreaker {
		t.Error("expected policy block to trip the circuit-breaker flag")
	}
	if dispatcher.callCount() != 0 {
		t.Errorf("disabled policy must not dispatch, got %d dispatches", dispatcher.callCount())
	}
}

// Test 6 — Exhausting retries trips the circuit breaker; subsequent cycles are
// blocked without further dispatches (spec #65).
func TestCircuitBreakerTripsAfterMaxAttempts(t *testing.T) {
	repo := newFakeNodeRepo()
	node := offlineNode("worker-01", domain.StatusReady)
	node.Role = domain.RoleWorker
	repo.add(node)

	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	dispatcher.mu.Lock()
	dispatcher.failures = 100 // every attempt fails
	dispatcher.mu.Unlock()

	engine, _ := testEngine(t, repo, dispatcher, recoveryTestConfig())

	first, err := engine.ReconcileNode(context.Background(), "worker-01")
	if err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if first.Result != domain.ReconciliationFailed {
		t.Errorf("expected RECONCILIATION_FAILED after exhausting retries, got %s", first.Result)
	}
	callsAfterFirst := dispatcher.callCount()

	second, err := engine.ReconcileNode(context.Background(), "worker-01")
	if err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}
	if second.RecoveryState != domain.RecoveryBlocked || !second.CircuitBreaker {
		t.Errorf("circuit breaker must block after exhaustion, got state=%s", second.RecoveryState)
	}
	if dispatcher.callCount() != callsAfterFirst {
		t.Error("blocked recovery must not dispatch anything")
	}
}

// Test 7 — Flapping nodes are blocked from repeated replacement (spec #68).
func TestFlappingDetectionBlocksRecovery(t *testing.T) {
	repo := newFakeNodeRepo()
	node := offlineNode("worker-01", domain.StatusReady)
	node.Role = domain.RoleWorker
	oldRecovery := nowValue.Add(-time.Hour)
	staleSuccess := oldRecovery.Add(-2 * time.Hour)
	node.FailureStreak = flappingThreshold
	node.LastRecoveryAt = &oldRecovery
	node.LastSuccessfulReconciliation = &staleSuccess
	repo.add(node)

	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, _ := testEngine(t, repo, dispatcher, recoveryTestConfig())

	result, err := engine.ReconcileNode(context.Background(), "worker-01")
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.RecoveryState != domain.RecoveryBlocked {
		t.Errorf("flapping node must be blocked, got %s", result.RecoveryState)
	}
	if dispatcher.callCount() != 0 {
		t.Errorf("flapping node must not be replaced, got %d dispatches", dispatcher.callCount())
	}
}

// Test 8 — Failure classification maps observed signals to categories.
func TestFailureClassification(t *testing.T) {
	repo := newFakeNodeRepo()
	node := offlineNode("worker-01", domain.StatusOffline) // stale + offline
	engine := &Reconciler{
		cfg: recoveryTestConfig(),
		now: func() time.Time { return nowValue },
	}
	_ = repo

	if got := engine.classifyFailure(node); got != domain.FailureInfrastructure {
		t.Errorf("stale+offline: expected INFRASTRUCTURE, got %s", got)
	}

	fresh := readyNode("worker-02")
	if got := engine.classifyFailure(fresh); got != domain.FailureUnknown {
		t.Errorf("healthy node: expected UNKNOWN, got %s", got)
	}

	noHeartbeat := readyNode("worker-03")
	noHeartbeat.LastHeartbeat = nil
	if got := engine.classifyFailure(noHeartbeat); got != domain.FailureAgent {
		t.Errorf("missing heartbeat: expected AGENT, got %s", got)
	}

	kubeBroken := readyNode("worker-04")
	kubeBroken.Kubernetes = &domain.KubernetesActualState{Available: false}
	if got := engine.classifyFailure(kubeBroken); got != domain.FailureKubernetes {
		t.Errorf("kubernetes unavailable: expected KUBERNETES, got %s", got)
	}

	staleReady := suspectNode("worker-05") // stale but still READY
	if got := engine.classifyFailure(staleReady); got != domain.FailureNetwork {
		t.Errorf("stale+ready: expected NETWORK, got %s", got)
	}
}

// Test 9 — PROVISION_REPLACEMENT is unsupported without a provisioner and
// delegated to one when wired (spec #20/#72).
func TestExecutorProvisionReplacement(t *testing.T) {
	repo := newFakeNodeRepo()
	repo.add(offlineNode("worker-01", domain.StatusReady))
	executor := NewReconciliationExecutor(nil, nil, repo)

	err := executor.Execute(context.Background(), Plan{Action: ActionProvisionReplacement, NodeID: "worker-01"})
	var unsupported *UnsupportedActionError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected UnsupportedActionError without provisioner, got %v", err)
	}

	provisioned := false
	fake := &fakeProvisioner{fn: func(_ context.Context, failed *domain.Node) (*domain.Node, error) {
		provisioned = true
		return readyNode("worker-new"), nil
	}}
	executor = NewReconciliationExecutor(nil, fake, repo)
	if err := executor.Execute(context.Background(), Plan{Action: ActionProvisionReplacement, NodeID: "worker-01"}); err != nil {
		t.Fatalf("replacement provisioning failed: %v", err)
	}
	if !provisioned {
		t.Error("expected provisioner to be invoked")
	}
}

type fakeProvisioner struct {
	fn func(ctx context.Context, failed *domain.Node) (*domain.Node, error)
}

func (f *fakeProvisioner) ProvisionReplacement(ctx context.Context, failed *domain.Node) (*domain.Node, error) {
	return f.fn(ctx, failed)
}

// Test 10 — Running reconciliation twice does not duplicate in-flight
// recoveries (idempotency, spec #87/#94).
func TestIdempotentDoubleReconcileNoDuplicateDispatch(t *testing.T) {
	repo := newFakeNodeRepo()
	node := offlineNode("worker-01", domain.StatusReady)
	node.Role = domain.RoleWorker
	repo.add(node)
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue.Add(-24 * time.Hour)}
	engine, _ := testEngine(t, repo, dispatcher, recoveryTestConfig())

	first, err := engine.ReconcileNode(context.Background(), "worker-01")
	if err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}
	if first.Result != domain.ReconciliationReconciling {
		// Dispatcher set actual READY with an old heartbeat, so the node is
		// still drifting; the recovery is in flight with a deadline.
		if first.Result != domain.ReconciliationReconciled {
			t.Fatalf("unexpected first result %s", first.Result)
		}
	}
	calls := dispatcher.callCount()

	second, err := engine.ReconcileNode(context.Background(), "worker-01")
	if err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}
	_ = second
	if dispatcher.callCount() > calls+1 {
		t.Errorf("duplicate reconcile duplicated dispatches: %d -> %d", calls, dispatcher.callCount())
	}
}

// Test 11 — DefaultRecoveryPolicy matches the conservative Phase 9 defaults,
// and zero-value policies inherit them.
func TestDefaultRecoveryPolicy(t *testing.T) {
	policy := DefaultRecoveryPolicy()
	if !policy.WorkerEnabled() {
		t.Error("worker recovery must be enabled by default")
	}
	if policy.ControlPlaneEnabled() {
		t.Error("control-plane recovery must be disabled by default")
	}
	if policy.MaxRecoveryAttempts != 3 {
		t.Errorf("default max attempts = %d, want 3", policy.MaxRecoveryAttempts)
	}
	if policy.MaxConcurrentRecoveries != 2 {
		t.Errorf("default max concurrent = %d, want 2", policy.MaxConcurrentRecoveries)
	}
	if policy.RecoveryCooldown <= 0 {
		t.Error("cooldown must be positive")
	}

	var zero RecoveryPolicy
	if !zero.WorkerEnabled() {
		t.Error("zero policy must default to worker recovery enabled")
	}
	if zero.ControlPlaneEnabled() {
		t.Error("zero policy must default to control-plane recovery disabled")
	}
	fmt.Fprint(io.Discard, "")
	log.SetOutput(io.Discard)
}
