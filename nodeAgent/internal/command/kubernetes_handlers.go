package command

import (
	"context"
	"strings"

	"AetherGrid/nodeAgent/internal/kubernetes"
)

// Kubernetes command types. The control plane references these constants when
// dispatching Kubernetes operations; the agent registers matching handlers.
const (
	// CommandGetKubernetesStatus asks the agent to report its observed
	// Kubernetes state.
	CommandGetKubernetesStatus = "GET_KUBERNETES_STATUS"
	// CommandListKubernetesNodes asks the agent to list Kubernetes nodes.
	CommandListKubernetesNodes = "LIST_KUBERNETES_NODES"
	// CommandListKubernetesPods asks the agent to list Kubernetes pods. An
	// optional namespace parameter narrows the query.
	CommandListKubernetesPods = "LIST_KUBERNETES_PODS"
	// CommandCreateTestNamespace creates a safe, reversible test namespace.
	CommandCreateTestNamespace = "CREATE_TEST_NAMESPACE"
	// CommandDeleteTestNamespace deletes the reversible test namespace.
	CommandDeleteTestNamespace = "DELETE_TEST_NAMESPACE"
)

// DefaultTestNamespace is the default name used by the reversible test
// namespace operations. Only this namespace (or a caller-provided safe name)
// is ever created or deleted.
const DefaultTestNamespace = "aether-grid-test"

// KubernetesService is the subset of the Kubernetes service the command
// handlers need. Command handlers never call client-go directly.
type KubernetesService interface {
	GetState(ctx context.Context) kubernetes.KubernetesState
	GetClusterInfo(ctx context.Context) (kubernetes.ClusterInfo, error)
	ListNodes(ctx context.Context) ([]kubernetes.KubernetesNode, error)
	GetNode(ctx context.Context, name string) (kubernetes.KubernetesNode, error)
	ListPods(ctx context.Context, namespace string) ([]kubernetes.KubernetesPod, error)
	CreateTestNamespace(ctx context.Context, name string) error
	DeleteTestNamespace(ctx context.Context, name string) error
}

// GetKubernetesStatusHandler answers GET_KUBERNETES_STATUS with the observed
// Kubernetes state. It never fails: disabled and unavailable clusters are
// reported as state.
type GetKubernetesStatusHandler struct {
	service KubernetesService
}

// NewGetKubernetesStatusHandler constructs the GET_KUBERNETES_STATUS handler.
func NewGetKubernetesStatusHandler(service KubernetesService) *GetKubernetesStatusHandler {
	return &GetKubernetesStatusHandler{service: service}
}

// Handle returns the observed Kubernetes state.
func (h *GetKubernetesStatusHandler) Handle(ctx context.Context, _ Request) (any, error) {
	if h.service == nil {
		return map[string]any{
			"available": false,
			"status":    string(kubernetes.KubernetesStatusUnavailable),
		}, nil
	}
	return h.service.GetState(ctx), nil
}

// ListKubernetesNodesHandler answers LIST_KUBERNETES_NODES.
type ListKubernetesNodesHandler struct {
	service KubernetesService
}

// NewListKubernetesNodesHandler constructs the LIST_KUBERNETES_NODES handler.
func NewListKubernetesNodesHandler(service KubernetesService) *ListKubernetesNodesHandler {
	return &ListKubernetesNodesHandler{service: service}
}

// Handle lists Kubernetes nodes.
func (h *ListKubernetesNodesHandler) Handle(ctx context.Context, _ Request) (any, error) {
	if h.service == nil {
		return nil, &kubernetes.Error{Code: kubernetes.CodeUnavailable}
	}
	nodes, err := h.service.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	return nodes, nil
}

// ListKubernetesPodsHandler answers LIST_KUBERNETES_PODS. An optional
// "namespace" parameter narrows the query.
type ListKubernetesPodsHandler struct {
	service KubernetesService
}

// NewListKubernetesPodsHandler constructs the LIST_KUBERNETES_PODS handler.
func NewListKubernetesPodsHandler(service KubernetesService) *ListKubernetesPodsHandler {
	return &ListKubernetesPodsHandler{service: service}
}

// Handle lists Kubernetes pods, optionally in one namespace.
func (h *ListKubernetesPodsHandler) Handle(ctx context.Context, request Request) (any, error) {
	if h.service == nil {
		return nil, &kubernetes.Error{Code: kubernetes.CodeUnavailable}
	}
	namespace, _ := request.Parameters["namespace"].(string)
	pods, err := h.service.ListPods(ctx, namespace)
	if err != nil {
		return nil, err
	}
	return pods, nil
}

// CreateTestNamespaceHandler answers CREATE_TEST_NAMESPACE. It only ever
// creates the safe test namespace (or a caller-provided validated name).
type CreateTestNamespaceHandler struct {
	service KubernetesService
}

// NewCreateTestNamespaceHandler constructs the CREATE_TEST_NAMESPACE handler.
func NewCreateTestNamespaceHandler(service KubernetesService) *CreateTestNamespaceHandler {
	return &CreateTestNamespaceHandler{service: service}
}

// Handle creates the test namespace.
func (h *CreateTestNamespaceHandler) Handle(ctx context.Context, request Request) (any, error) {
	if h.service == nil {
		return nil, &kubernetes.Error{Code: kubernetes.CodeUnavailable}
	}
	name := namespaceName(request)
	if err := h.service.CreateTestNamespace(ctx, name); err != nil {
		return nil, err
	}
	return map[string]any{"namespace": name, "message": "test namespace created"}, nil
}

// DeleteTestNamespaceHandler answers DELETE_TEST_NAMESPACE. It only ever
// deletes the safe test namespace (or a caller-provided validated name).
type DeleteTestNamespaceHandler struct {
	service KubernetesService
}

// NewDeleteTestNamespaceHandler constructs the DELETE_TEST_NAMESPACE handler.
func NewDeleteTestNamespaceHandler(service KubernetesService) *DeleteTestNamespaceHandler {
	return &DeleteTestNamespaceHandler{service: service}
}

// Handle deletes the test namespace.
func (h *DeleteTestNamespaceHandler) Handle(ctx context.Context, request Request) (any, error) {
	if h.service == nil {
		return nil, &kubernetes.Error{Code: kubernetes.CodeUnavailable}
	}
	name := namespaceName(request)
	if err := h.service.DeleteTestNamespace(ctx, name); err != nil {
		return nil, err
	}
	return map[string]any{"namespace": name, "message": "test namespace deleted"}, nil
}

func namespaceName(request Request) string {
	if raw, ok := request.Parameters["name"].(string); ok {
		if name := strings.TrimSpace(raw); name != "" {
			return name
		}
	}
	return DefaultTestNamespace
}
