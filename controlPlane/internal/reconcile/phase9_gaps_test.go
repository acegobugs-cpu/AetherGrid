package reconcile

import (
	"context"
	"errors"
	"math/rand"
	"testing"
	"time"

	"AetherGrid/controlPlane/internal/domain"
)

// TestBackoffJitterBounds verifies jitter never exceeds the computed delay and
// stays within [delay/2, delay].
func TestBackoffJitterBounds(t *testing.T) {
	base := 10 * time.Second
	max := 90 * time.Second
	for attempt := 1; attempt <= 8; attempt++ {
		delay := backoff(attempt, base, max)
		if delay > max {
			t.Fatalf("attempt %d: backoff %s exceeds max %s", attempt, delay, max)
		}
		for i := 0; i < 100; i++ {
			jittered := backoffJittered(delay, testRng)
			if jittered > delay {
				t.Fatalf("attempt %d: jitter %s exceeds delay %s", attempt, jittered, delay)
			}
			half := delay / 2
			if jittered < half {
				t.Fatalf("attempt %d: jitter %s below half %s", attempt, jittered, half)
			}
		}
	}
}

var testRng = newTestRand()

// TestSemaphoreBoundedConcurrency proves at most `limit` holders exist.
func TestSemaphoreBoundedConcurrency(t *testing.T) {
	sem := newSemaphore(2)
	acquired := make(chan struct{}, 10)
	release := make(chan struct{})

	for i := 0; i < 5; i++ {
		go func() {
			if !sem.acquire(context.Background()) {
				return
			}
			acquired <- struct{}{}
			<-release
			sem.release()
		}()
	}

	// Exactly two goroutines get through immediately.
	deadline := time.After(time.Second)
	got := 0
	for got < 2 {
		select {
		case <-acquired:
			got++
		case <-deadline:
			t.Fatalf("only %d of 2 slots acquired", got)
		}
	}
	select {
	case <-acquired:
		t.Fatal("third holder acquired beyond the limit")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
}

// TestPreconditionsBlockUnmanagedNodes verifies recovery is blocked for nodes
// that do not belong to a managed cluster or whose cluster has a conflicting
// operation (Phase 9 #70/#36).
type fakeClusterInspector struct {
	cluster     *domain.Cluster
	conflicting bool
}

func (f *fakeClusterInspector) ForNode(_ context.Context, nodeID string) (*domain.Cluster, error) {
	if f.cluster == nil {
		return nil, errors.New("no managed cluster")
	}
	return f.cluster, nil
}

func (f *fakeClusterInspector) HasConflictingOperation(_ context.Context, _ string) (bool, error) {
	return f.conflicting, nil
}

func TestPreconditionsBlockConflictingOperation(t *testing.T) {
	repo := newFakeNodeRepo()
	node := offlineNode("worker-01", domain.StatusReady)
	node.Role = domain.RoleWorker
	repo.add(node)
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, _ := testEngine(t, repo, dispatcher, recoveryTestConfig())
	engine.SetClusterInspector(&fakeClusterInspector{
		cluster: &domain.Cluster{
			ID: "cluster-01",
			Spec: domain.ClusterSpec{
				Name:             "edge-cluster-01",
				K3sVersion:       "v1.31.0",
				ControlPlaneNode: "cp-01",
				WorkerNodes:      []string{"worker-01"},
			},
		},
		conflicting: true,
	})

	result, err := engine.ReconcileNode(context.Background(), "worker-01")
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.RecoveryState != domain.RecoveryBlocked || !result.CircuitBreaker {
		t.Errorf("expected blocked by precondition, got state=%s breaker=%v",
			result.RecoveryState, result.CircuitBreaker)
	}
	if dispatcher.callCount() != 0 {
		t.Errorf("precondition failure must not dispatch, got %d", dispatcher.callCount())
	}
}

func TestPreconditionsAllowManagedHealthyCluster(t *testing.T) {
	repo := newFakeNodeRepo()
	node := offlineNode("worker-01", domain.StatusReady)
	node.Role = domain.RoleWorker
	repo.add(node)
	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue}
	engine, _ := testEngine(t, repo, dispatcher, recoveryTestConfig())
	engine.SetClusterInspector(&fakeClusterInspector{
		cluster: &domain.Cluster{
			ID:   "cluster-01",
			Spec: domain.ClusterSpec{Name: "c", K3sVersion: "v1", ControlPlaneNode: "cp-01", WorkerNodes: []string{"worker-01"}},
		},
	})

	result, err := engine.ReconcileNode(context.Background(), "worker-01")
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.CircuitBreaker || dispatcher.callCount() == 0 {
		t.Errorf("managed cluster member should recover, state=%s dispatches=%d",
			result.RecoveryState, dispatcher.callCount())
	}
}

// TestResetRecovery clears the circuit breaker so reconciliation re-evaluates
// the failure (Phase 9 #100). Reset must not execute any action itself.
func TestResetRecovery(t *testing.T) {
	repo := newFakeNodeRepo()
	node := offlineNode("worker-01", domain.StatusReady)
	node.Role = domain.RoleWorker
	node.RecoveryState = domain.RecoveryBlocked
	node.RecoveryAttempts = 99
	node.FailureStreak = flappingThreshold + 1
	repo.add(node)

	dispatcher := &fakeDispatcher{repo: repo, heartbeat: nowValue.Add(-24 * time.Hour)}
	engine, _ := testEngine(t, repo, dispatcher, recoveryTestConfig())

	// Blocked before reset.
	before, err := engine.ReconcileNode(context.Background(), "worker-01")
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if !before.CircuitBreaker {
		t.Fatal("expected blocked state before reset")
	}

	if err := engine.ResetRecovery(context.Background(), "worker-01"); err != nil {
		t.Fatalf("reset failed: %v", err)
	}
	stored, _ := repo.GetByID(context.Background(), "worker-01")
	if stored.RecoveryAttempts != 0 || stored.FailureStreak != 0 ||
		stored.RecoveryState != domain.RecoveryNotRequired {
		t.Errorf("reset did not clear state: %+v", stored)
	}

	// After reset the failure is evaluated again and recovery may run.
	after, err := engine.ReconcileNode(context.Background(), "worker-01")
	if err != nil {
		t.Fatalf("post-reset reconcile failed: %v", err)
	}
	if after.CircuitBreaker {
		t.Error("reset must clear the circuit breaker")
	}
	if dispatcher.callCount() < 1 {
		t.Error("post-reset cycle should be allowed to act on confirmed failure")
	}
}
func newTestRand() *rand.Rand {
	return rand.New(rand.NewSource(42))
}
