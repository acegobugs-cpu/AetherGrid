package kubernetes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// KubernetesClient is the abstraction over the Kubernetes API used by the
// AETHER-GRID agent. All client-go usage lives behind this interface so the
// reconciliation engine and command handlers depend on an abstraction, not on
// Kubernetes directly.
type KubernetesClient interface {
	// GetClusterInfo returns a summary of the cluster: version, node counts.
	GetClusterInfo(ctx context.Context) (ClusterInfo, error)
	// ListNodes returns the observed Kubernetes nodes.
	ListNodes(ctx context.Context) ([]KubernetesNode, error)
	// GetNode returns a single Kubernetes node by name.
	GetNode(ctx context.Context, name string) (KubernetesNode, error)
	// ListPods returns pods. An empty namespace lists pods in all namespaces.
	ListPods(ctx context.Context, namespace string) ([]KubernetesPod, error)
	// CreateNamespace creates a namespace. It returns a domain error on
	// failure.
	CreateNamespace(ctx context.Context, name string) error
	// DeleteNamespace deletes a namespace. It returns a domain error on
	// failure.
	DeleteNamespace(ctx context.Context, name string) error
}

// Client is the client-go backed implementation of KubernetesClient. A single
// client is constructed once and reused for the lifetime of the agent.
type Client struct {
	clientset *kubernetes.Clientset
	discovery discovery.DiscoveryInterface
}

// NewClient builds a KubernetesClient from the configured kubeconfig. It
// resolves configuration in the following order:
//
//  1. An explicit KUBECONFIG path when configured.
//  2. The standard kubeconfig loading rules (the KUBECONFIG environment
//     variable and $HOME/.kube/config).
//  3. In-cluster configuration, so the same client abstraction is reusable
//     when the agent itself runs inside Kubernetes.
//
// It returns CodeInvalidConfig when no usable configuration can be loaded.
func NewClient(kubeconfigPath string) (*Client, error) {
	config, err := loadRestConfig(kubeconfigPath)
	if err != nil {
		return nil, &Error{Code: CodeInvalidConfig, Err: err}
	}
	return newClientFromConfig(config)
}

// newClientFromConfig constructs the client-go backed Client. It is shared by
// NewClient and tests.
func newClientFromConfig(config *rest.Config) (*Client, error) {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, &Error{Code: CodeInvalidConfig, Err: err}
	}

	return &Client{
		clientset: clientset,
		discovery: discovery.NewDiscoveryClient(clientset.RESTClient()),
	}, nil
}

func loadRestConfig(kubeconfigPath string) (*rest.Config, error) {
	if strings.TrimSpace(kubeconfigPath) != "" {
		config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("loading kubeconfig %q: %w", kubeconfigPath, err)
		}
		return config, nil
	}

	// Standard loading rules honour the KUBECONFIG environment variable and
	// the default ~/.kube/config location.
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if home := homeDir(); home != "" {
		rules.ExplicitPath = filepath.Join(home, ".kube", "config")
	}
	overrides := &clientcmd.ConfigOverrides{}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err == nil {
		return config, nil
	}

	// Fall back to in-cluster configuration for future in-cluster deployments.
	inCluster, inClusterErr := rest.InClusterConfig()
	if inClusterErr == nil {
		return inCluster, nil
	}

	return nil, fmt.Errorf("no usable Kubernetes configuration: %v (in-cluster: %v)", err, inClusterErr)
}

func homeDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	return ""
}

// GetClusterInfo returns the cluster version and node counts.
func (c *Client) GetClusterInfo(ctx context.Context) (ClusterInfo, error) {
	version, err := c.discovery.ServerVersion()
	if err != nil {
		return ClusterInfo{}, Translate(err)
	}

	nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return ClusterInfo{}, Translate(err)
	}

	ready := 0
	for _, node := range nodes.Items {
		if nodeReady(node) {
			ready++
		}
	}

	return ClusterInfo{
		Version:       version.GitVersion,
		NodeCount:     len(nodes.Items),
		ReadyNodes:    ready,
		NotReadyNodes: len(nodes.Items) - ready,
	}, nil
}

// ListNodes returns the observed Kubernetes nodes.
func (c *Client) ListNodes(ctx context.Context) ([]KubernetesNode, error) {
	nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, Translate(err)
	}

	result := make([]KubernetesNode, 0, len(nodes.Items))
	for _, node := range nodes.Items {
		result = append(result, mapNode(node))
	}
	return result, nil
}

// GetNode returns a single node by name.
func (c *Client) GetNode(ctx context.Context, name string) (KubernetesNode, error) {
	node, err := c.clientset.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return KubernetesNode{}, Translate(err)
	}
	return mapNode(*node), nil
}

// ListPods returns pods, optionally filtered to a namespace. An empty
// namespace lists pods in every namespace; the agent only requests a bounded
// summary so this stays inexpensive.
func (c *Client) ListPods(ctx context.Context, namespace string) ([]KubernetesPod, error) {
	pods, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, Translate(err)
	}

	result := make([]KubernetesPod, 0, len(pods.Items))
	for _, pod := range pods.Items {
		restarts := 0
		for _, status := range pod.Status.ContainerStatuses {
			restarts += int(status.RestartCount)
		}
		result = append(result, KubernetesPod{
			Namespace:    pod.Namespace,
			Name:         pod.Name,
			Phase:        string(pod.Status.Phase),
			NodeName:     pod.Spec.NodeName,
			RestartCount: restarts,
		})
	}
	return result, nil
}

// CreateNamespace creates a namespace for the safe, reversible test-namespace
// operation.
func (c *Client) CreateNamespace(ctx context.Context, name string) error {
	namespace := &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if _, err := c.clientset.CoreV1().Namespaces().Create(ctx, namespace, metav1.CreateOptions{}); err != nil {
		return Translate(err)
	}
	return nil
}

// DeleteNamespace removes a namespace created by the test-namespace operation.
func (c *Client) DeleteNamespace(ctx context.Context, name string) error {
	if err := c.clientset.CoreV1().Namespaces().Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return Translate(err)
	}
	return nil
}

func mapNode(node v1.Node) KubernetesNode {
	mapped := KubernetesNode{
		Name:              node.Name,
		KubernetesVersion: node.Status.NodeInfo.KubeletVersion,
		OS:                node.Status.NodeInfo.OperatingSystem,
		Architecture:      node.Status.NodeInfo.Architecture,
		Ready:             nodeReady(node),
	}
	for _, address := range node.Status.Addresses {
		if address.Type == "InternalIP" {
			mapped.InternalIP = address.Address
			break
		}
	}
	for label := range node.Labels {
		if strings.HasPrefix(label, "node-role.kubernetes.io/") {
			mapped.Roles = append(mapped.Roles, strings.TrimPrefix(label, "node-role.kubernetes.io/"))
		}
	}
	return mapped
}

func nodeReady(node v1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == "Ready" {
			return condition.Status == "True"
		}
	}
	return false
}
