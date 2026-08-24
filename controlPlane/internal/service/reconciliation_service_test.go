package service

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	"AetherGrid/controlPlane/internal/domain"
	"AetherGrid/controlPlane/internal/reconcile"
)

func testReconciler(t *testing.T) (*ReconciliationService, *mockNodeRepository) {
	t.Helper()

	repo := newMockNodeRepository()
	history := newMockReconciliationHistory()
	dispatcher := &mockDispatcher{nodes: repo}
	logger := log.New(io.Discard, "", 0)

	reconciler := NewReconciliationService(reconcile.Config{
		Interval:         time.Minute,
		Workers:          1,
		HeartbeatTimeout: 30 * time.Second,
		MaxRetries:       3,
		MaxBackoff:       time.Second,
		RecoveryTimeout:  time.Minute,
	}, repo, history, dispatcher, logger, nil)

	return reconciler, repo
}

func TestReconciliationServiceInSync(t *testing.T) {
	reconciler, repo := testReconciler(t)
	nodeSvc := NewNodeService(repo)

	created, err := nodeSvc.Create(context.Background(), CreateNodeInput{Name: "edge-01"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	if _, err := nodeSvc.SetDesiredStatus(context.Background(), created.ID, created.Status); err != nil {
		t.Fatalf("set desired status failed: %v", err)
	}

	result, err := reconciler.Reconcile(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if result.Result != domain.ReconciliationInSync {
		t.Errorf("expected %s, got %s", domain.ReconciliationInSync, result.Result)
	}
	if len(result.Differences) != 0 {
		t.Errorf("expected no differences, got %v", result.Differences)
	}
}

func TestReconciliationServiceDriftDetected(t *testing.T) {
	reconciler, repo := testReconciler(t)
	nodeSvc := NewNodeService(repo)

	created, err := nodeSvc.Create(context.Background(), CreateNodeInput{Name: "edge-01"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Initial node is PROVISIONING while desired is READY; PROVISIONING is a
	// transitional status so no corrective action applies.
	result, err := reconciler.Reconcile(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if result.Result != domain.ReconciliationDriftDetected {
		t.Errorf("expected %s, got %s", domain.ReconciliationDriftDetected, result.Result)
	}
	if len(result.Differences) != 1 {
		t.Errorf("expected 1 difference, got %v", result.Differences)
	}
	if result.Differences[0].Field != domain.FieldStatus {
		t.Errorf("expected status difference, got %+v", result.Differences[0])
	}
}

func TestReconciliationServiceNotFound(t *testing.T) {
	reconciler, _ := testReconciler(t)

	if _, err := reconciler.Reconcile(context.Background(), "missing"); !IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestReconciliationServiceHistory(t *testing.T) {
	reconciler, repo := testReconciler(t)
	nodeSvc := NewNodeService(repo)

	created, err := nodeSvc.Create(context.Background(), CreateNodeInput{Name: "edge-01"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// A drift detection persists a history row.
	if _, err := reconciler.Reconcile(context.Background(), created.ID); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	events, err := reconciler.History(context.Background(), created.ID, 10)
	if err != nil {
		t.Fatalf("history failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 history row, got %d", len(events))
	}
	if events[0].Result != domain.ReconciliationDriftDetected {
		t.Errorf("expected DRIFT_DETECTED row, got %s", events[0].Result)
	}
}

// mockDispatcher dispatches RESTART_AGENT commands against an in-memory node
// repository.
type mockDispatcher struct {
	nodes *mockNodeRepository
}

func (d *mockDispatcher) DispatchRestart(ctx context.Context, nodeID string) (*domain.Command, error) {
	if _, err := d.nodes.GetByID(ctx, nodeID); err != nil {
		return nil, err
	}
	return &domain.Command{ID: "cmd", NodeID: nodeID, Type: domain.CommandRestartAgent}, nil
}

// mockReconciliationHistory is an in-memory
// repository.ReconciliationHistoryRepository for tests.
type mockReconciliationHistory struct {
	events []*domain.ReconciliationEvent
}

func newMockReconciliationHistory() *mockReconciliationHistory {
	return &mockReconciliationHistory{}
}

func (m *mockReconciliationHistory) Create(_ context.Context, event *domain.ReconciliationEvent) error {
	m.events = append(m.events, event)
	return nil
}

func (m *mockReconciliationHistory) GetLatest(_ context.Context, _ string) (*domain.ReconciliationEvent, error) {
	if len(m.events) == 0 {
		return nil, nil
	}
	return m.events[len(m.events)-1], nil
}

func (m *mockReconciliationHistory) ListByNode(_ context.Context, _ string, limit int) ([]*domain.ReconciliationEvent, error) {
	if limit > len(m.events) {
		limit = len(m.events)
	}
	events := make([]*domain.ReconciliationEvent, 0, limit)
	for i := len(m.events) - 1; i >= 0 && len(events) < limit; i-- {
		events = append(events, m.events[i])
	}
	return events, nil
}
