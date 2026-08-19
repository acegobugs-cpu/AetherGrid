package kubernetes

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"testing"
	"time"
)

// mockClient is a configurable in-memory KubernetesClient for tests.
type mockClient struct {
	clusterInfo    ClusterInfo
	clusterInfoErr error
	nodes          []KubernetesNode
	nodesErr       error
	pods           []KubernetesPod
	podsErr        error
	createErr      error
	deleteErr      error
	created        []string
	deleted        []string
}

func (m *mockClient) GetClusterInfo(context.Context) (ClusterInfo, error) {
	return m.clusterInfo, m.clusterInfoErr
}

func (m *mockClient) ListNodes(context.Context) ([]KubernetesNode, error) {
	return m.nodes, m.nodesErr
}

func (m *mockClient) GetNode(_ context.Context, _ string) (KubernetesNode, error) {
	if len(m.nodes) > 0 {
		return m.nodes[0], m.nodesErr
	}
	return KubernetesNode{}, m.nodesErr
}

func (m *mockClient) ListPods(_ context.Context, _ string) ([]KubernetesPod, error) {
	return m.pods, m.podsErr
}

func (m *mockClient) CreateNamespace(_ context.Context, name string) error {
	m.created = append(m.created, name)
	return m.createErr
}

func (m *mockClient) DeleteNamespace(_ context.Context, name string) error {
	m.deleted = append(m.deleted, name)
	return m.deleteErr
}

func quietLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}

func testService(client KubernetesClient) *Service {
	service := NewService(ServiceConfig{
		Enabled:        true,
		RequestTimeout: 5 * time.Second,
	}, nil, quietLogger())
	service.SetClient(client)
	return service
}

func TestServiceGetStateReady(t *testing.T) {
	client := &mockClient{
		clusterInfo: ClusterInfo{Version: "v1.31.0", NodeCount: 2, ReadyNodes: 2},
		pods: []KubernetesPod{
			{Phase: "Running"}, {Phase: "Running"}, {Phase: "Failed"}, {Phase: "Pending"},
		},
	}
	service := testService(client)

	state := service.GetState(context.Background())
	if state.Status != KubernetesStatusReady {
		t.Fatalf("expected READY, got %s", state.Status)
	}
	if !state.Available {
		t.Fatal("expected available")
	}
	if state.Version != "v1.31.0" {
		t.Errorf("unexpected version %q", state.Version)
	}
	if state.Workload.TotalPods != 4 || state.Workload.RunningPods != 2 || state.Workload.FailedPods != 1 {
		t.Errorf("unexpected workload: %+v", state.Workload)
	}
}

func TestServiceGetStateDegraded(t *testing.T) {
	client := &mockClient{
		clusterInfo: ClusterInfo{Version: "v1.31.0", NodeCount: 3, ReadyNodes: 2, NotReadyNodes: 1},
	}
	service := testService(client)

	state := service.GetState(context.Background())
	if state.Status != KubernetesStatusDegraded {
		t.Fatalf("expected DEGRADED, got %s", state.Status)
	}
}

func TestServiceGetStateUnavailable(t *testing.T) {
	client := &mockClient{clusterInfoErr: &Error{Code: CodeUnavailable}}
	service := testService(client)

	state := service.GetState(context.Background())
	if state.Status != KubernetesStatusUnavailable {
		t.Fatalf("expected UNAVAILABLE, got %s", state.Status)
	}
	if state.Available {
		t.Fatal("expected not available")
	}
}

func TestServiceGetStateDisabled(t *testing.T) {
	service := NewService(ServiceConfig{Enabled: false}, nil, quietLogger())
	state := service.GetState(context.Background())
	if state.Status != KubernetesStatusDisabled {
		t.Fatalf("expected DISABLED, got %s", state.Status)
	}
	if state.Available {
		t.Fatal("expected not available")
	}
}

func TestServiceGetStatePodSummaryFailureDoesNotDegradeCluster(t *testing.T) {
	client := &mockClient{
		clusterInfo: ClusterInfo{Version: "v1.31.0", NodeCount: 1, ReadyNodes: 1},
		podsErr:     &Error{Code: CodeForbidden},
	}
	service := testService(client)

	state := service.GetState(context.Background())
	if state.Status != KubernetesStatusReady {
		t.Fatalf("expected READY despite pod summary failure, got %s", state.Status)
	}
}

func TestServiceClientReused(t *testing.T) {
	calls := 0
	client := &mockClient{clusterInfo: ClusterInfo{NodeCount: 1, ReadyNodes: 1}}
	service := NewService(ServiceConfig{Enabled: true, RequestTimeout: time.Second}, func() (KubernetesClient, error) {
		calls++
		return client, nil
	}, quietLogger())

	for i := 0; i < 3; i++ {
		if _, err := service.ListNodes(context.Background()); err != nil {
			t.Fatalf("ListNodes: %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("expected the client to be constructed once, got %d calls", calls)
	}
}

func TestServiceErrorTranslation(t *testing.T) {
	client := &mockClient{nodesErr: errors.New("connection refused")}
	service := testService(client)

	_, err := service.ListNodes(context.Background())
	if !IsCode(err, CodeUnavailable) {
		t.Fatalf("expected CodeUnavailable, got %v", err)
	}
}

func TestServiceDisabledOperations(t *testing.T) {
	service := NewService(ServiceConfig{Enabled: false}, nil, quietLogger())

	if _, err := service.ListNodes(context.Background()); !IsCode(err, CodeInvalidConfig) {
		t.Fatalf("expected CodeInvalidConfig for disabled ListNodes, got %v", err)
	}
	if err := service.CreateTestNamespace(context.Background(), "x"); !IsCode(err, CodeInvalidConfig) {
		t.Fatalf("expected CodeInvalidConfig for disabled create, got %v", err)
	}
}

func TestServiceNamespaceValidation(t *testing.T) {
	service := testService(&mockClient{})

	for _, name := range []string{"", "Kube", "kube-system", "has_underscore", strings.Repeat("a", 64)} {
		if err := service.CreateTestNamespace(context.Background(), name); !IsCode(err, CodeInvalidConfig) {
			t.Errorf("expected invalid configuration for %q, got %v", name, err)
		}
	}
	if err := service.CreateTestNamespace(context.Background(), "aether-grid-test"); err != nil {
		t.Errorf("expected valid namespace, got %v", err)
	}
}
