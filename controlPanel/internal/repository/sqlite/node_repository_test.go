package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
	"github.com/acegobugs-cpu/AetherGrid/internal/repository"
	"github.com/acegobugs-cpu/AetherGrid/migrations"

	"github.com/google/uuid"
)

func newTestRepository(t *testing.T) *NodeRepository {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	repo, err := NewNodeRepository(path)
	if err != nil {
		t.Fatalf("opening repository: %v", err)
	}
	t.Cleanup(func() { repo.Close() })

	if err := migrations.Apply(context.Background(), repo.DB()); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}
	return repo
}

func sampleNode(t *testing.T) *domain.Node {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	return &domain.Node{
		ID:                "f47ac10b-58cc-4372-a567-0e02b2c3d479",
		Name:              "edge-01",
		Location:          "addis-01",
		IPAddress:         "10.0.0.10",
		Status:            domain.StatusProvisioning,
		DesiredStatus:     domain.StatusReady,
		KubernetesEnabled: true,
		WireGuardEnabled:  true,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func TestRepositoryCreateAndGet(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	node := sampleNode(t)
	if err := repo.Create(ctx, node); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	got, err := repo.GetByID(ctx, node.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.ID != node.ID || got.Name != node.Name || got.Status != node.Status {
		t.Errorf("unexpected node: %+v", got)
	}
	if !got.KubernetesEnabled || !got.WireGuardEnabled {
		t.Error("expected flags to round-trip as enabled")
	}
	if !got.CreatedAt.Equal(node.CreatedAt) {
		t.Errorf("expected created_at %v, got %v", node.CreatedAt, got.CreatedAt)
	}
}

func TestRepositoryGetMissing(t *testing.T) {
	repo := newTestRepository(t)

	if _, err := repo.GetByID(context.Background(), "missing"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRepositoryCreateDuplicateName(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	first := sampleNode(t)
	second := sampleNode(t)
	second.ID = "8d5d6c1a-2f8b-4e7d-9a3c-1b2c3d4e5f6a"
	if err := repo.Create(ctx, first); err != nil {
		t.Fatalf("first create failed: %v", err)
	}
	if err := repo.Create(ctx, second); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestRepositoryUpdate(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	node := sampleNode(t)
	if err := repo.Create(ctx, node); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	node.Status = domain.StatusReady
	node.DesiredStatus = domain.StatusReady
	now := time.Now().UTC().Truncate(time.Microsecond)
	node.LastHeartbeat = &now
	node.UpdatedAt = now

	if err := repo.Update(ctx, node); err != nil {
		t.Fatalf("update failed: %v", err)
	}

	got, err := repo.GetByID(ctx, node.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.Status != domain.StatusReady {
		t.Errorf("expected status READY, got %q", got.Status)
	}
	if got.LastHeartbeat == nil || !got.LastHeartbeat.Equal(now) {
		t.Errorf("expected last_heartbeat %v, got %v", now, got.LastHeartbeat)
	}
}

func TestRepositoryUpdateMissing(t *testing.T) {
	repo := newTestRepository(t)

	node := sampleNode(t)
	node.ID = "missing"
	if err := repo.Update(context.Background(), node); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// TestRepositoryKubernetesRoundTrip verifies the Phase 4 Kubernetes columns are
// persisted and restored through Create, Update and Get.
func TestRepositoryKubernetesRoundTrip(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	node := sampleNode(t)
	node.KubernetesEnabled = true
	node.KubernetesMinimumReadyNodes = 2
	reported := time.Now().UTC().Truncate(time.Microsecond)
	node.Kubernetes = &domain.KubernetesActualState{
		Available:     true,
		Status:        domain.KubernetesDegraded,
		Version:       "v1.31.0",
		NodeCount:     3,
		ReadyNodes:    2,
		NotReadyNodes: 1,
		Workload: domain.WorkloadSummary{
			TotalPods:   10,
			RunningPods: 8,
			FailedPods:  2,
		},
		ReportedAt: reported,
	}
	if err := repo.Create(ctx, node); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	got, err := repo.GetByID(ctx, node.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.Kubernetes == nil {
		t.Fatal("expected kubernetes state to be restored")
	}
	if got.Kubernetes.Status != domain.KubernetesDegraded || !got.Kubernetes.Available {
		t.Errorf("unexpected kubernetes state: %+v", got.Kubernetes)
	}
	if got.Kubernetes.ReadyNodes != 2 || got.Kubernetes.NotReadyNodes != 1 || got.Kubernetes.NodeCount != 3 {
		t.Errorf("unexpected node counts: %+v", got.Kubernetes)
	}
	if got.Kubernetes.Workload.TotalPods != 10 || got.Kubernetes.Workload.RunningPods != 8 || got.Kubernetes.Workload.FailedPods != 2 {
		t.Errorf("unexpected workload: %+v", got.Kubernetes.Workload)
	}
	if !got.Kubernetes.ReportedAt.Equal(reported) {
		t.Errorf("expected reported_at %v, got %v", reported, got.Kubernetes.ReportedAt)
	}
	if got.KubernetesMinimumReadyNodes != 2 {
		t.Errorf("expected kubernetes_minimum_ready_nodes 2, got %d", got.KubernetesMinimumReadyNodes)
	}

	// Update the observed state and re-read.
	got.Kubernetes.Status = domain.KubernetesReady
	got.Kubernetes.ReadyNodes = 3
	got.Kubernetes.NotReadyNodes = 0
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	after, err := repo.GetByID(ctx, node.ID)
	if err != nil {
		t.Fatalf("get after update failed: %v", err)
	}
	if after.Kubernetes.Status != domain.KubernetesReady || after.Kubernetes.ReadyNodes != 3 || after.Kubernetes.NotReadyNodes != 0 {
		t.Errorf("expected updated kubernetes state, got %+v", after.Kubernetes)
	}
}

// TestRepositoryKubernetesNilState stores a nil observed state and restores nil.
func TestRepositoryKubernetesNilState(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	node := sampleNode(t)
	node.KubernetesEnabled = true
	if err := repo.Create(ctx, node); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	got, err := repo.GetByID(ctx, node.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.Kubernetes != nil {
		t.Fatalf("expected nil kubernetes state, got %+v", got.Kubernetes)
	}
}

func TestRepositoryList(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		node := sampleNode(t)
		node.ID = uuid.NewString()
		node.Name = "edge-" + string(rune('0'+i))
		if err := repo.Create(ctx, node); err != nil {
			t.Fatalf("create %d failed: %v", i, err)
		}
	}

	nodes, err := repo.GetAll(ctx)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(nodes))
	}
}

func TestRepositoryDelete(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	node := sampleNode(t)
	if err := repo.Create(ctx, node); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	if err := repo.Delete(ctx, node.ID); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if _, err := repo.GetByID(ctx, node.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
	if err := repo.Delete(ctx, node.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for double delete, got %v", err)
	}
}

func TestRepositoryPersistenceAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "persist.db")

	repo, err := NewNodeRepository(path)
	if err != nil {
		t.Fatalf("opening repository: %v", err)
	}
	if err := migrations.Apply(ctx, repo.DB()); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}

	node := sampleNode(t)
	if err := repo.Create(ctx, node); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if err := repo.Close(); err != nil {
		t.Fatalf("closing repository: %v", err)
	}

	reopened, err := NewNodeRepository(path)
	if err != nil {
		t.Fatalf("reopening repository: %v", err)
	}
	defer reopened.Close()

	got, err := reopened.GetByID(ctx, node.ID)
	if err != nil {
		t.Fatalf("get after reopen failed: %v", err)
	}
	if got.ID != node.ID || got.Name != node.Name {
		t.Errorf("expected persisted node, got %+v", got)
	}
}
