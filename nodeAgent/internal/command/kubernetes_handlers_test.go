package command

import (
	"context"
	"errors"
	"testing"

	"AetherGrid/nodeAgent/internal/kubernetes"
)

// fakeKubernetesService is an in-memory KubernetesService for handler tests.
type fakeKubernetesService struct {
	state       kubernetes.KubernetesState
	nodes       []kubernetes.KubernetesNode
	nodesErr    error
	pods        []kubernetes.KubernetesPod
	podsErr     error
	lastNS      string
	namespaceOp error
}

func (f *fakeKubernetesService) GetState(context.Context) kubernetes.KubernetesState {
	return f.state
}

func (f *fakeKubernetesService) GetClusterInfo(context.Context) (kubernetes.ClusterInfo, error) {
	return kubernetes.ClusterInfo{Version: f.state.Version, NodeCount: f.state.NodeCount}, nil
}

func (f *fakeKubernetesService) ListNodes(context.Context) ([]kubernetes.KubernetesNode, error) {
	return f.nodes, f.nodesErr
}

func (f *fakeKubernetesService) GetNode(_ context.Context, _ string) (kubernetes.KubernetesNode, error) {
	if len(f.nodes) > 0 {
		return f.nodes[0], f.nodesErr
	}
	return kubernetes.KubernetesNode{}, f.nodesErr
}

func (f *fakeKubernetesService) ListPods(_ context.Context, _ string) ([]kubernetes.KubernetesPod, error) {
	return f.pods, f.podsErr
}

func (f *fakeKubernetesService) CreateTestNamespace(_ context.Context, name string) error {
	f.lastNS = name
	return f.namespaceOp
}

func (f *fakeKubernetesService) DeleteTestNamespace(_ context.Context, name string) error {
	f.lastNS = name
	return f.namespaceOp
}

func TestGetKubernetesStatusHandler(t *testing.T) {
	service := &fakeKubernetesService{state: kubernetes.KubernetesState{
		Available: true, Status: kubernetes.KubernetesStatusReady, Version: "v1.31.0", NodeCount: 1, ReadyNodes: 1,
	}}
	handler := NewGetKubernetesStatusHandler(service)

	result, err := handler.Handle(context.Background(), Request{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	state, ok := result.(kubernetes.KubernetesState)
	if !ok {
		t.Fatalf("expected KubernetesState, got %T", result)
	}
	if state.Status != kubernetes.KubernetesStatusReady {
		t.Fatalf("expected READY, got %s", state.Status)
	}
}

func TestListKubernetesNodesHandler(t *testing.T) {
	service := &fakeKubernetesService{nodes: []kubernetes.KubernetesNode{{Name: "edge-1", Ready: true}}}
	handler := NewListKubernetesNodesHandler(service)

	result, err := handler.Handle(context.Background(), Request{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	nodes := result.([]kubernetes.KubernetesNode)
	if len(nodes) != 1 || nodes[0].Name != "edge-1" {
		t.Fatalf("unexpected nodes: %+v", nodes)
	}
}

func TestListKubernetesPodsHandlerNamespaceParam(t *testing.T) {
	service := &fakeKubernetesService{pods: []kubernetes.KubernetesPod{{Namespace: "default", Name: "web-1"}}}
	handler := NewListKubernetesPodsHandler(service)

	result, err := handler.Handle(context.Background(), Request{Parameters: map[string]any{"namespace": "default"}})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	pods := result.([]kubernetes.KubernetesPod)
	if len(pods) != 1 || pods[0].Name != "web-1" {
		t.Fatalf("unexpected pods: %+v", pods)
	}
}

func TestListKubernetesPodsHandlerError(t *testing.T) {
	service := &fakeKubernetesService{podsErr: &kubernetes.Error{Code: kubernetes.CodeForbidden}}
	handler := NewListKubernetesPodsHandler(service)

	_, err := handler.Handle(context.Background(), Request{})
	if !kubernetes.IsCode(err, kubernetes.CodeForbidden) {
		t.Fatalf("expected CodeForbidden, got %v", err)
	}
}

func TestCreateDeleteTestNamespaceHandlers(t *testing.T) {
	service := &fakeKubernetesService{}
	create := NewCreateTestNamespaceHandler(service)
	if _, err := create.Handle(context.Background(), Request{}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if service.lastNS != DefaultTestNamespace {
		t.Errorf("expected default namespace, got %q", service.lastNS)
	}

	deleteHandler := NewDeleteTestNamespaceHandler(service)
	if _, err := deleteHandler.Handle(context.Background(), Request{Parameters: map[string]any{"name": "custom-ns"}}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if service.lastNS != "custom-ns" {
		t.Errorf("expected custom namespace, got %q", service.lastNS)
	}
}

func TestKubernetesHandlersNilService(t *testing.T) {
	status := NewGetKubernetesStatusHandler(nil)
	state, err := status.Handle(context.Background(), Request{})
	if err != nil {
		t.Fatalf("status handle: %v", err)
	}
	if _, ok := state.(map[string]any); !ok {
		t.Fatalf("expected status map, got %T", state)
	}

	for _, handler := range []Handler{
		NewListKubernetesNodesHandler(nil),
		NewListKubernetesPodsHandler(nil),
		NewCreateTestNamespaceHandler(nil),
		NewDeleteTestNamespaceHandler(nil),
	} {
		if _, err := handler.Handle(context.Background(), Request{}); err == nil {
			t.Fatal("expected error from nil-service handler")
		}
	}
}

func TestUnsupportedNamespaceName(t *testing.T) {
	service := &fakeKubernetesService{namespaceOp: errors.New("boom")}
	handler := NewCreateTestNamespaceHandler(service)
	if _, err := handler.Handle(context.Background(), Request{}); err == nil {
		t.Fatal("expected propagated error")
	}
}
