package service

import (
	"context"
	"testing"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
)

func TestReconciliationInSync(t *testing.T) {
	repo := newMockNodeRepository()
	nodeSvc := NewNodeService(repo)
	reconciler := NewReconciliationService(repo)

	created, err := nodeSvc.Create(context.Background(), CreateNodeInput{Name: "edge-01"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Set desired state to match the actual state.
	if _, err := nodeSvc.SetDesiredStatus(context.Background(), created.ID, created.Status); err != nil {
		t.Fatalf("set desired status failed: %v", err)
	}

	result, err := reconciler.Reconcile(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if result.Result != ResultInSync {
		t.Errorf("expected %s, got %s", ResultInSync, result.Result)
	}
	if result.DesiredState != result.ActualState {
		t.Errorf("expected states to match: desired=%s actual=%s", result.DesiredState, result.ActualState)
	}
	if result.NodeID != created.ID {
		t.Errorf("expected node id %s, got %s", created.ID, result.NodeID)
	}
}

func TestReconciliationDriftDetected(t *testing.T) {
	repo := newMockNodeRepository()
	nodeSvc := NewNodeService(repo)
	reconciler := NewReconciliationService(repo)

	created, err := nodeSvc.Create(context.Background(), CreateNodeInput{Name: "edge-01"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// The initial node is PROVISIONING while desired is READY, so this
	// should already be drift.
	result, err := reconciler.Reconcile(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	if result.Result != ResultDriftDetected {
		t.Errorf("expected %s, got %s", ResultDriftDetected, result.Result)
	}
	if result.DesiredState != domain.StatusReady {
		t.Errorf("expected desired READY, got %s", result.DesiredState)
	}
	if result.ActualState != domain.InitialStatus {
		t.Errorf("expected actual %s, got %s", domain.InitialStatus, result.ActualState)
	}
}

func TestReconciliationNotFound(t *testing.T) {
	reconciler := NewReconciliationService(newMockNodeRepository())

	if _, err := reconciler.Reconcile(context.Background(), "missing"); !IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestReconciliationConverges(t *testing.T) {
	repo := newMockNodeRepository()
	nodeSvc := NewNodeService(repo)
	reconciler := NewReconciliationService(repo)

	created, err := nodeSvc.Create(context.Background(), CreateNodeInput{Name: "edge-01"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Setting the same desired state repeatedly must converge on IN_SYNC.
	for i := 0; i < 3; i++ {
		if _, err := nodeSvc.SetDesiredStatus(context.Background(), created.ID, created.Status); err != nil {
			t.Fatalf("set desired status %d failed: %v", i, err)
		}
		result, err := reconciler.Reconcile(context.Background(), created.ID)
		if err != nil {
			t.Fatalf("reconcile %d failed: %v", i, err)
		}
		if result.Result != ResultInSync {
			t.Errorf("reconcile %d: expected %s, got %s", i, ResultInSync, result.Result)
		}
	}
}
