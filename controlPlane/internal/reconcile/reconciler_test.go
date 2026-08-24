package reconcile

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"AetherGrid/controlPlane/internal/domain"
	"AetherGrid/controlPlane/internal/repository"
)

// fakeNodeRepo is an in-memory node repository whose nodes can be mutated to
// simulate agent behavior.
type fakeNodeRepo struct {
	mu    sync.Mutex
	nodes map[string]*domain.Node
}

func newFakeNodeRepo() *fakeNodeRepo {
	return &fakeNodeRepo{nodes: make(map[string]*domain.Node)}
}

func (f *fakeNodeRepo) add(node *domain.Node) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nodes[node.ID] = node
}

func (f *fakeNodeRepo) GetByID(_ context.Context, id string) (*domain.Node, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	node, ok := f.nodes[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	copied := *node
	return &copied, nil
}

func (f *fakeNodeRepo) GetAll(_ context.Context) ([]*domain.Node, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	nodes := make([]*domain.Node, 0, len(f.nodes))
	for _, node := range f.nodes {
		copied := *node
		nodes = append(nodes, &copied)
	}
	return nodes, nil
}

func (f *fakeNodeRepo) UpdateReconciliation(_ context.Context, node *domain.Node) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored, ok := f.nodes[node.ID]
	if !ok {
		return repository.ErrNotFound
	}
	stored.LastReconciliation = node.LastReconciliation
	stored.LastSuccessfulReconciliation = node.LastSuccessfulReconciliation
	stored.LastReconciliationResult = node.LastReconciliationResult
	stored.LastReconciliationAction = node.LastReconciliationAction
	stored.LastReconciliationError = node.LastReconciliationError
	stored.LastReconciliationDeadline = node.LastReconciliationDeadline
	stored.ReconciliationAttempts = node.ReconciliationAttempts
	stored.RecoveryState = node.RecoveryState
	stored.RecoveryFailure = node.RecoveryFailure
	stored.RecoveryAttempts = node.RecoveryAttempts
	stored.LastRecoveryAt = node.LastRecoveryAt
	stored.NextRetryAt = node.NextRetryAt
	stored.FailureStreak = node.FailureStreak
	stored.UpdatedAt = node.UpdatedAt
	return nil
}

// setActual changes the observed state of a stored node to simulate an agent
// reporting through the normal service path.
func (f *fakeNodeRepo) setActual(id string, status domain.NodeStatus, lastHeartbeat time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if node, ok := f.nodes[id]; ok {
		node.Status = status
		node.LastHeartbeat = &lastHeartbeat
	}
}

// fakeDispatcher is a command dispatcher with controllable failure behavior.
type fakeDispatcher struct {
	mu        sync.Mutex
	failures  int
	calls     int
	repo      *fakeNodeRepo
	heartbeat time.Time
}

func (d *fakeDispatcher) DispatchRestart(ctx context.Context, nodeID string) (*domain.Command, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	if d.failures > 0 {
		d.failures--
		return nil, &RetryableError{Err: context.DeadlineExceeded}
	}
	// Simulate the agent recovering and reporting READY with a fresh heartbeat.
	d.repo.setActual(nodeID, domain.StatusReady, d.heartbeat)
	return &domain.Command{ID: "cmd-" + nodeID, NodeID: nodeID, Type: domain.CommandRestartAgent}, nil
}

func (d *fakeDispatcher) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

// fakeHistory collects persisted reconciliation events.
type fakeHistory struct {
	mu     sync.Mutex
	events []*domain.ReconciliationEvent
}

func (f *fakeHistory) create(ctx context.Context, event *domain.ReconciliationEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
	return nil
}

func (f *fakeHistory) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

// countByResult counts persisted rows carrying the given result value.
func (f *fakeHistory) countByResult(result domain.ReconciliationStatus) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, event := range f.events {
		if event.Result == result {
			n++
		}
	}
	return n
}

// testEngine builds a Reconciler wired to fakes with an injectable now and a
// no-op sleep so retries do not actually wait.
func testEngine(t *testing.T, repo *fakeNodeRepo, dispatcher *fakeDispatcher, cfg Config) (*Reconciler, *fakeHistory) {
	t.Helper()
	observer := NewRepositoryObserver(repo, cfg.HeartbeatTimeout, func() time.Time { return nowValue })
	planner := NewReconciliationPlanner()
	executor := NewReconciliationExecutor(dispatcher, nil, nil)
	history := &fakeHistory{}
	logger := log.New(io.Discard, "", 0)

	engine := NewReconciler(observer, planner, executor, repo, history.create, cfg, logger,
		func() time.Time { return nowValue },
		func(ctx context.Context, _ time.Duration) error { return ctx.Err() })
	return engine, history
}

// nowValue is the fixed clock used by tests.
var nowValue = time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)

// offlineNode returns a node whose heartbeat is far in the past so the
// observer marks it stale/OFFLINE.
func offlineNode(id string, desired domain.NodeStatus) *domain.Node {
	oldHeartbeat := nowValue.Add(-24 * time.Hour)
	return &domain.Node{
		ID:            id,
		Name:          id,
		Status:        domain.StatusOffline,
		DesiredStatus: desired,
		LastHeartbeat: &oldHeartbeat,
		CreatedAt:     nowValue,
		UpdatedAt:     nowValue,
	}
}

// readyNode returns a healthy node in sync with its desired state.
func readyNode(id string) *domain.Node {
	heartbeat := nowValue.Add(-time.Second)
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

// Test 1 — Already synchronized.
func TestReconcileInSync(t *testing.T) {
	repo := newFakeNodeRepo()
	repo.add(readyNode("edge-01"))
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, _ := testEngine(t, repo, dispatcher, Config{
		Interval: time.Second, Workers: 2, HeartbeatTimeout: 30 * time.Second,
		MaxRetries: 3, MaxBackoff: time.Second, RecoveryTimeout: time.Minute,
	})

	result, err := engine.ReconcileNode(context.Background(), "edge-01")
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.Result != domain.ReconciliationInSync {
		t.Errorf("expected IN_SYNC, got %s", result.Result)
	}
	if len(result.Differences) != 0 {
		t.Errorf("expected no differences, got %v", result.Differences)
	}
	if dispatcher.callCount() != 0 {
		t.Errorf("expected no dispatch, got %d", dispatcher.callCount())
	}
}

// Test 2 — Drift detected (detect-only mode, no corrective action executed).
func TestReconcileDriftDetected(t *testing.T) {
	repo := newFakeNodeRepo()
	repo.add(offlineNode("edge-01", domain.StatusReady))
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, _ := testEngine(t, repo, dispatcher, Config{
		Interval: time.Second, Workers: 2, HeartbeatTimeout: 30 * time.Second,
		MaxRetries: 0, MaxBackoff: time.Second, RecoveryTimeout: time.Minute,
	})

	result, err := engine.ReconcileNode(context.Background(), "edge-01")
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.Result != domain.ReconciliationDriftDetected {
		t.Errorf("expected DRIFT_DETECTED, got %s", result.Result)
	}
	if len(result.Differences) != 1 {
		t.Errorf("expected 1 difference, got %v", result.Differences)
	}
	if result.Differences[0].Field != domain.FieldStatus {
		t.Errorf("expected status difference, got %+v", result.Differences[0])
	}
	if dispatcher.callCount() != 0 {
		t.Errorf("expected no dispatch in detect-only mode, got %d", dispatcher.callCount())
	}
}

// Test 3 — Successful recovery: the dispatched action brings the node back and
// the engine verifies convergence.
func TestReconcileSuccessfulRecovery(t *testing.T) {
	repo := newFakeNodeRepo()
	repo.add(offlineNode("edge-01", domain.StatusReady))
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, history := testEngine(t, repo, dispatcher, Config{
		Interval: time.Second, Workers: 2, HeartbeatTimeout: 30 * time.Second,
		MaxRetries: 3, MaxBackoff: time.Second, RecoveryTimeout: time.Minute,
	})

	result, err := engine.ReconcileNode(context.Background(), "edge-01")
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.Result != domain.ReconciliationReconciled {
		t.Errorf("expected RECONCILED, got %s", result.Result)
	}
	if result.Action != ActionRecoverNode {
		t.Errorf("expected action RECOVER_NODE, got %s", result.Action)
	}
	if dispatcher.callCount() != 1 {
		t.Errorf("expected 1 dispatch, got %d", dispatcher.callCount())
	}
	// One cycle result row plus Phase 9 audit rows (RECOVERY_STARTED,
	// NODE_REJOINED, RECOVERY_COMPLETED) share the history table.
	if got := history.count(); got < 1 {
		t.Errorf("expected at least 1 history row, got %d", got)
	}
	if got := history.countByResult(domain.ReconciliationReconciled); got != 1 {
		t.Errorf("expected 1 RECONCILED result row, got %d", got)
	}
	if got := history.countByResult(domain.AuditEventResult); got < 2 {
		t.Errorf("expected recovery audit rows, got %d", got)
	}
	if repo.nodes["edge-01"].Status != domain.StatusReady {
		t.Error("expected node to have recovered to READY")
	}
}

// Test 3b — Successful recovery emits the Phase 9 audit trail.
// Test 4 — Action failure: the action keeps failing and the engine records
// RECONCILIATION_FAILED with attempts and error.
func TestReconcileActionFailure(t *testing.T) {
	repo := newFakeNodeRepo()
	repo.add(offlineNode("edge-01", domain.StatusReady))
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, history := testEngine(t, repo, dispatcher, Config{
		Interval: time.Second, Workers: 2, HeartbeatTimeout: 30 * time.Second,
		MaxRetries: 3, MaxBackoff: time.Second, RecoveryTimeout: time.Minute,
	})
	dispatcher.failures = 100

	result, err := engine.ReconcileNode(context.Background(), "edge-01")
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.Result != domain.ReconciliationFailed {
		t.Errorf("expected RECONCILIATION_FAILED, got %s", result.Result)
	}
	if result.Action != ActionRecoverNode {
		t.Errorf("expected action RECOVER_NODE, got %s", result.Action)
	}
	if result.Attempt != 3 {
		t.Errorf("expected 3 attempts, got %d", result.Attempt)
	}
	if result.Error == "" {
		t.Error("expected an error message")
	}
	if !result.Retryable {
		t.Error("expected retryable failure")
	}
	if dispatcher.callCount() != 3 {
		t.Errorf("expected 3 dispatches, got %d", dispatcher.callCount())
	}
	if history.count() == 0 {
		t.Error("expected failure history rows")
	}
	if repo.nodes["edge-01"].LastReconciliationResult != domain.ReconciliationFailed {
		t.Error("expected node metadata to record RECONCILIATION_FAILED")
	}
}

// Test 5 — Retry: the first two attempts fail, the third succeeds.
func TestReconcileRetry(t *testing.T) {
	repo := newFakeNodeRepo()
	repo.add(offlineNode("edge-01", domain.StatusReady))
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, history := testEngine(t, repo, dispatcher, Config{
		Interval: time.Second, Workers: 2, HeartbeatTimeout: 30 * time.Second,
		MaxRetries: 3, MaxBackoff: time.Second, RecoveryTimeout: time.Minute,
	})
	dispatcher.failures = 2

	result, err := engine.ReconcileNode(context.Background(), "edge-01")
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.Result != domain.ReconciliationReconciled {
		t.Errorf("expected RECONCILED after retries, got %s", result.Result)
	}
	if result.Attempt != 3 {
		t.Errorf("expected success on attempt 3, got attempt %d", result.Attempt)
	}
	if dispatcher.callCount() != 3 {
		t.Errorf("expected 3 dispatches, got %d", dispatcher.callCount())
	}
	if history.count() == 0 {
		t.Error("expected history rows")
	}
	if repo.nodes["edge-01"].Status != domain.StatusReady {
		t.Error("expected node to have recovered to READY")
	}
}

// TestReconcileNotFound — reconciling a node that does not exist.
func TestReconcileNotFound(t *testing.T) {
	repo := newFakeNodeRepo()
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, _ := testEngine(t, repo, dispatcher, Config{
		Interval: time.Second, Workers: 1, HeartbeatTimeout: 30 * time.Second,
		MaxRetries: 3, MaxBackoff: time.Second, RecoveryTimeout: time.Minute,
	})

	if _, err := engine.ReconcileNode(context.Background(), "missing"); err == nil {
		t.Fatal("expected an error for a missing node")
	}
}

// TestInProgressRecoveryNotRedispatched — a node whose recovery is still within
// its deadline is not dispatched again.
func TestInProgressRecoveryNotRedispatched(t *testing.T) {
	repo := newFakeNodeRepo()
	repo.add(offlineNode("edge-01", domain.StatusReady))
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, _ := testEngine(t, repo, dispatcher, Config{
		Interval: time.Second, Workers: 2, HeartbeatTimeout: 30 * time.Second,
		MaxRetries: 3, MaxBackoff: time.Second, RecoveryTimeout: time.Minute,
	})

	// Mark the node as already RECONCILING with a deadline in the future.
	deadline := nowValue.Add(time.Minute)
	repo.nodes["edge-01"].LastReconciliationResult = domain.ReconciliationReconciling
	repo.nodes["edge-01"].LastReconciliationAction = ActionRecoverNode
	repo.nodes["edge-01"].LastReconciliationDeadline = &deadline
	repo.nodes["edge-01"].ReconciliationAttempts = 1

	result, err := engine.ReconcileNode(context.Background(), "edge-01")
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.Result != domain.ReconciliationReconciling {
		t.Errorf("expected RECONCILING, got %s", result.Result)
	}
	if dispatcher.callCount() != 0 {
		t.Errorf("expected no re-dispatch while recovery is in progress, got %d", dispatcher.callCount())
	}
}

// TestTransitionalStatusIsDrift — a transitional status produces drift without
// a recovery action.
func TestTransitionalStatusIsDrift(t *testing.T) {
	repo := newFakeNodeRepo()
	heartbeat := nowValue.Add(-time.Second)
	repo.add(&domain.Node{
		ID: "edge-01", Name: "edge-01",
		Status: domain.StatusProvisioning, DesiredStatus: domain.StatusReady,
		LastHeartbeat: &heartbeat, CreatedAt: nowValue, UpdatedAt: nowValue,
	})
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, _ := testEngine(t, repo, dispatcher, Config{
		Interval: time.Second, Workers: 2, HeartbeatTimeout: 30 * time.Second,
		MaxRetries: 3, MaxBackoff: time.Second, RecoveryTimeout: time.Minute,
	})

	result, err := engine.ReconcileNode(context.Background(), "edge-01")
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.Result != domain.ReconciliationDriftDetected {
		t.Errorf("expected DRIFT_DETECTED, got %s", result.Result)
	}
	if result.Action != "" {
		t.Errorf("expected no action for transitional status, got %s", result.Action)
	}
	if dispatcher.callCount() != 0 {
		t.Errorf("expected no dispatch for transitional status, got %d", dispatcher.callCount())
	}
}

// TestHeartbeatStalenessMarksOffline — a node with a stale heartbeat is
// treated as OFFLINE even if its stored status is READY.
func TestHeartbeatStalenessMarksOffline(t *testing.T) {
	repo := newFakeNodeRepo()
	heartbeat := nowValue.Add(-24 * time.Hour)
	repo.add(&domain.Node{
		ID: "edge-01", Name: "edge-01",
		Status: domain.StatusReady, DesiredStatus: domain.StatusReady,
		LastHeartbeat: &heartbeat, CreatedAt: nowValue, UpdatedAt: nowValue,
	})
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, _ := testEngine(t, repo, dispatcher, Config{
		Interval: time.Second, Workers: 2, HeartbeatTimeout: 30 * time.Second,
		MaxRetries: 3, MaxBackoff: time.Second, RecoveryTimeout: time.Minute,
	})

	result, err := engine.ReconcileNode(context.Background(), "edge-01")
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.Result != domain.ReconciliationReconciled {
		t.Errorf("expected recovery of a stale node, got %s", result.Result)
	}
	if dispatcher.callCount() != 1 {
		t.Errorf("expected 1 dispatch for stale node, got %d", dispatcher.callCount())
	}
}

// TestConcurrentDifferentNodes — distinct nodes reconcile concurrently.
func TestConcurrentDifferentNodes(t *testing.T) {
	repo := newFakeNodeRepo()
	repo.add(offlineNode("edge-01", domain.StatusReady))
	repo.add(offlineNode("edge-02", domain.StatusReady))
	repo.add(offlineNode("edge-03", domain.StatusReady))
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, _ := testEngine(t, repo, dispatcher, Config{
		Interval: time.Second, Workers: 4, HeartbeatTimeout: 30 * time.Second,
		MaxRetries: 3, MaxBackoff: time.Second, RecoveryTimeout: time.Minute,
	})

	var wg sync.WaitGroup
	for _, id := range []string{"edge-01", "edge-02", "edge-03"} {
		wg.Add(1)
		go func(nodeID string) {
			defer wg.Done()
			if _, err := engine.ReconcileNode(context.Background(), nodeID); err != nil {
				t.Errorf("reconcile %s failed: %v", nodeID, err)
			}
		}(id)
	}
	wg.Wait()

	if dispatcher.callCount() != 3 {
		t.Errorf("expected 3 dispatches, got %d", dispatcher.callCount())
	}
}

// TestConcurrentSameNodeSerialized — a node reconciled from many goroutines is
// never dispatched concurrently (the dispatcher serializes and would deadlock
// or miscount otherwise). This asserts the per-node lock prevents duplicate
// in-flight work: with many goroutines, only sequential dispatches occur and
// the final state converges.
func TestConcurrentSameNodeSerialized(t *testing.T) {
	repo := newFakeNodeRepo()
	repo.add(offlineNode("edge-01", domain.StatusReady))
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, _ := testEngine(t, repo, dispatcher, Config{
		Interval: time.Second, Workers: 8, HeartbeatTimeout: 30 * time.Second,
		MaxRetries: 1, MaxBackoff: time.Second, RecoveryTimeout: time.Minute,
	})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = engine.ReconcileNode(context.Background(), "edge-01")
		}()
	}
	wg.Wait()

	// First goroutine recovers the node; the rest observe READY and do nothing.
	if dispatcher.callCount() != 1 {
		t.Errorf("expected exactly 1 dispatch for the same node, got %d", dispatcher.callCount())
	}
}

// TestContextCancellationDuringRetry — a cancelled context aborts a retry loop.
func TestContextCancellationDuringRetry(t *testing.T) {
	repo := newFakeNodeRepo()
	repo.add(offlineNode("edge-01", domain.StatusReady))
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	dispatcher.failures = 100
	engine, _ := testEngine(t, repo, dispatcher, Config{
		Interval: time.Second, Workers: 1, HeartbeatTimeout: 30 * time.Second,
		MaxRetries: 3, MaxBackoff: time.Second, RecoveryTimeout: time.Minute,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// The sleep hook returns ctx.Err() immediately, so the retry loop must
	// stop without retrying past the first attempt.
	result, err := engine.ReconcileNode(ctx, "edge-01")
	if err == nil {
		t.Fatalf("expected cancellation error, got result %+v", result)
	}
}

// TestDetectOnlyRecordsMetadata — detect-only mode records drift metadata.
func TestDetectOnlyRecordsMetadata(t *testing.T) {
	repo := newFakeNodeRepo()
	repo.add(offlineNode("edge-01", domain.StatusReady))
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, _ := testEngine(t, repo, dispatcher, Config{
		Interval: time.Second, Workers: 1, HeartbeatTimeout: 30 * time.Second,
		MaxRetries: 0, MaxBackoff: time.Second, RecoveryTimeout: time.Minute,
	})

	if _, err := engine.ReconcileNode(context.Background(), "edge-01"); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if repo.nodes["edge-01"].LastReconciliationResult != domain.ReconciliationDriftDetected {
		t.Errorf("expected DRIFT_DETECTED metadata, got %s",
			repo.nodes["edge-01"].LastReconciliationResult)
	}
}

// TestMetricsCounters — the metrics counters track completed cycles.
func TestMetricsCounters(t *testing.T) {
	repo := newFakeNodeRepo()
	repo.add(readyNode("edge-01"))
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, _ := testEngine(t, repo, dispatcher, Config{
		Interval: time.Second, Workers: 1, HeartbeatTimeout: 30 * time.Second,
		MaxRetries: 3, MaxBackoff: time.Second, RecoveryTimeout: time.Minute,
	})

	if _, err := engine.ReconcileNode(context.Background(), "edge-01"); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if got := engine.Metrics().CyclesInSync.Load(); got != 1 {
		t.Errorf("expected 1 in-sync cycle, got %d", got)
	}
	if got := engine.Metrics().NodesReconciled.Load(); got != 1 {
		t.Errorf("expected 1 reconciled node, got %d", got)
	}
}

// TestBackoff — exponential backoff grows towards the maximum.
func TestBackoff(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
	}
	for _, test := range tests {
		if got := backoff(test.attempt, time.Second, 10*time.Second); got != test.want {
			t.Errorf("backoff(%d) = %s, want %s", test.attempt, got, test.want)
		}
	}
	if got := backoff(10, time.Second, 10*time.Second); got != 10*time.Second {
		t.Errorf("backoff should cap at max, got %s", got)
	}
}

// TestWorkQueueDedup — enqueuing the same node multiple times coalesces.
func TestWorkQueueDedup(t *testing.T) {
	queue := newWorkQueue()
	queue.Enqueue("edge-01")
	queue.Enqueue("edge-01")
	queue.Enqueue("edge-02")
	if got := queue.Len(); got != 2 {
		t.Fatalf("expected 2 pending, got %d", got)
	}
	first := queue.Dequeue()
	second := queue.Dequeue()
	if first == second {
		t.Error("expected distinct nodes")
	}
	if queue.Dequeue() != "" {
		t.Error("expected empty queue")
	}
}

// TestExecutorUnsupportedAction — unsupported actions fail explicitly.
func TestExecutorUnsupportedAction(t *testing.T) {
	executor := NewReconciliationExecutor(&fakeDispatcher{}, nil, nil)
	err := executor.Execute(context.Background(), Plan{Action: ActionEnableKubernetes})
	var unsupported *UnsupportedActionError
	if !errors.As(err, &unsupported) {
		t.Fatalf("expected UnsupportedActionError, got %v", err)
	}
	if err.Error() != ErrTextUnsupportedAction+": "+ActionEnableKubernetes {
		t.Errorf("unexpected error text: %s", err.Error())
	}
}

// TestPlannerRecoverNode — an OFFLINE node plans a recovery.
func TestPlannerRecoverNode(t *testing.T) {
	planner := NewReconciliationPlanner()
	old := nowValue.Add(-24 * time.Hour)
	plan, err := planner.Plan(context.Background(), Strategy{
		NodeID:  "edge-01",
		Desired: domain.DesiredState{Status: domain.StatusReady},
		Actual:  domain.ActualState{Status: domain.StatusOffline, LastHeartbeat: &old},
		Stale:   true,
	})
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if plan.Action != ActionRecoverNode {
		t.Errorf("expected RECOVER_NODE, got %s", plan.Action)
	}
	if !plan.NeedsAction() {
		t.Error("expected plan to need action")
	}
}

// TestPlannerKubernetesDrift — Kubernetes drift is surfaced as a structured
// difference but produces no executable action in Phase 4 (DRIFT_DETECTED).
func TestPlannerKubernetesDrift(t *testing.T) {
	planner := NewReconciliationPlanner()
	plan, err := planner.Plan(context.Background(), Strategy{
		NodeID: "edge-01",
		Desired: domain.DesiredState{
			Status: domain.StatusReady,
			Kubernetes: domain.KubernetesDesiredState{
				Enabled:           true,
				MinimumReadyNodes: 1,
			},
		},
		Actual: domain.ActualState{
			Status: domain.StatusReady,
			Kubernetes: &domain.KubernetesActualState{
				Available:  true,
				Status:     domain.KubernetesDegraded,
				ReadyNodes: 0,
			},
		},
	})
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(plan.Differences) != 1 || plan.Differences[0].Field != domain.FieldKubernetesReadyNodes {
		t.Fatalf("expected kubernetes.ready_nodes difference, got %v", plan.Differences)
	}
	if plan.Action != "" {
		t.Errorf("expected no corrective action for Kubernetes drift, got %q", plan.Action)
	}
	if plan.NeedsAction() {
		t.Error("expected plan not to need an action")
	}
}

// TestPlannerKubernetesUnavailable — an unavailable cluster with an enabled
// Kubernetes expectation is surfaced as kubernetes.available drift.
func TestPlannerKubernetesUnavailable(t *testing.T) {
	planner := NewReconciliationPlanner()
	plan, err := planner.Plan(context.Background(), Strategy{
		NodeID: "edge-01",
		Desired: domain.DesiredState{
			Status: domain.StatusReady,
			Kubernetes: domain.KubernetesDesiredState{
				Enabled:           true,
				MinimumReadyNodes: 1,
			},
		},
		Actual: domain.ActualState{
			Status:     domain.StatusReady,
			Kubernetes: &domain.KubernetesActualState{Available: false, Status: domain.KubernetesUnavailable},
		},
	})
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(plan.Differences) != 1 || plan.Differences[0].Field != domain.FieldKubernetesAvailable {
		t.Fatalf("expected kubernetes.available difference, got %v", plan.Differences)
	}
	if plan.Action != "" {
		t.Errorf("expected no corrective action, got %q", plan.Action)
	}
}

// TestPlannerKubernetesDesiredDisabled — Kubernetes is not enforced when not
// desired, even if the observed cluster is unavailable.
func TestPlannerKubernetesDesiredDisabled(t *testing.T) {
	planner := NewReconciliationPlanner()
	plan, err := planner.Plan(context.Background(), Strategy{
		NodeID:  "edge-01",
		Desired: domain.DesiredState{Status: domain.StatusReady},
		Actual: domain.ActualState{
			Status:     domain.StatusReady,
			Kubernetes: &domain.KubernetesActualState{Available: false, Status: domain.KubernetesUnavailable},
		},
	})
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(plan.Differences) != 0 {
		t.Fatalf("expected no differences when Kubernetes is not desired, got %v", plan.Differences)
	}
}

// TestObserverStaleness — the observer flags stale heartbeats.
func TestObserverStaleness(t *testing.T) {
	repo := newFakeNodeRepo()
	fresh := nowValue.Add(-time.Second)
	repo.add(&domain.Node{
		ID: "fresh", Name: "fresh", Status: domain.StatusReady,
		DesiredStatus: domain.StatusReady, LastHeartbeat: &fresh,
		CreatedAt: nowValue, UpdatedAt: nowValue,
	})
	stale := nowValue.Add(-time.Hour)
	repo.add(&domain.Node{
		ID: "stale", Name: "stale", Status: domain.StatusReady,
		DesiredStatus: domain.StatusReady, LastHeartbeat: &stale,
		CreatedAt: nowValue, UpdatedAt: nowValue,
	})

	observer := NewRepositoryObserver(repo, 30*time.Second, func() time.Time { return nowValue })
	strategies, err := observer.Observe(context.Background())
	if err != nil {
		t.Fatalf("observe failed: %v", err)
	}

	byID := make(map[string]Strategy)
	for _, strategy := range strategies {
		byID[strategy.NodeID] = strategy
	}
	if byID["fresh"].Stale {
		t.Error("expected fresh node to be healthy")
	}
	if !byID["stale"].Stale {
		t.Error("expected stale node to be flagged")
	}
}

// asError reports whether err can be matched to the target.
func asError(err error, target any) bool {
	return errorsAs(err, target)
}

// errorsAs is a thin wrapper around errors.As so the test reads cleanly.
var errorsAs = func(err error, target any) bool {
	return errorAs(err, target)
}

func errorAs(err error, target any) bool {
	if err == nil {
		return false
	}
	switch t := target.(type) {
	case **UnsupportedActionError:
		unsupported, ok := err.(*UnsupportedActionError)
		if ok {
			*t = unsupported
		}
		return ok
	default:
		return false
	}
}

// TestPendingWorkReporting — pending work is visible through the engine.
func TestPendingWorkReporting(t *testing.T) {
	engine := &Reconciler{queue: newWorkQueue()}
	engine.queue.Enqueue("edge-01")
	if got := engine.PendingWork(); got != 1 {
		t.Errorf("expected 1 pending, got %d", got)
	}
}

// TestStartStopEngine — starting and stopping the engine is idempotent and
// clean.
func TestStartStopEngine(t *testing.T) {
	repo := newFakeNodeRepo()
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, _ := testEngine(t, repo, dispatcher, Config{
		Interval: 10 * time.Millisecond, Workers: 2, HeartbeatTimeout: 30 * time.Second,
		MaxRetries: 1, MaxBackoff: time.Millisecond, RecoveryTimeout: time.Second,
	})

	engine.Start()
	engine.Start() // idempotent
	time.Sleep(20 * time.Millisecond)
	engine.Stop()
	engine.Stop() // idempotent
}
