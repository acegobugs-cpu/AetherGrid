package bootstrapper

import (
	"context"
)

// KubernetesBootstrapStatus is the status of a Kubernetes bootstrap operation.
type KubernetesBootstrapStatus string

// Kubernetes bootstrap statuses.
const (
	K8sBootstrapStatusPending   KubernetesBootstrapStatus = "PENDING"
	K8sBootstrapStatusRunning   KubernetesBootstrapStatus = "RUNNING"
	K8sBootstrapStatusSucceeded KubernetesBootstrapStatus = "SUCCEEDED"
	K8sBootstrapStatusFailed    KubernetesBootstrapStatus = "FAILED"
	K8sBootstrapStatusCancelled KubernetesBootstrapStatus = "CANCELLED"
)

// KubernetesBootstrapper is the abstraction responsible for bootstrapping a
// Kubernetes cluster on top of AETHER-GRID edge nodes. It owns the Kubernetes
// distribution installation, control-plane initialization, worker join, and
// cluster verification. The exact implementation is K3sBootstrapper.
//
// This abstraction isolates Kubernetes-specific operations from the rest of
// AETHER-GRID, allowing future distributions (kubeadm, RKE2) without
// redesigning the control plane.
type KubernetesBootstrapper interface {
	// InstallServer installs the Kubernetes distribution on the control-plane node.
	InstallServer(ctx context.Context, clusterID, nodeID string) (*BootstrapOperationResult, error)

	// InitializeServer initializes the Kubernetes control plane and returns the
	// cluster join information. The returned token and endpoint must be kept
	// secret; callers are responsible for secure handling.
	InitializeServer(ctx context.Context, clusterID, nodeID string) (*ClusterJoinInfo, error)

	// InstallAgent installs the Kubernetes agent on a worker node.
	InstallAgent(ctx context.Context, clusterID, nodeID string) (*BootstrapOperationResult, error)

	// JoinWorker configures and starts the Kubernetes agent on a worker node,
	// joining it to the existing cluster. The join info must be retrieved from
	// InitializeServer and must not be exposed through public APIs or logs.
	JoinWorker(ctx context.Context, clusterID, nodeID string, joinInfo *ClusterJoinInfo) (*BootstrapOperationResult, error)

	// GetClusterStatus returns the current Kubernetes cluster status by querying
	// the Kubernetes API. It is authoritative for Kubernetes runtime state.
	GetClusterStatus(ctx context.Context, clusterID string) (*ClusterStatusInfo, error)

	// VerifyCluster performs a comprehensive verification of the cluster:
	// API reachability, server readiness, worker membership, label presence,
	// and version matching.
	VerifyCluster(ctx context.Context, clusterID string) (*BootstrapOperationResult, error)

	// RemoveNode removes a node from the Kubernetes cluster.
	RemoveNode(ctx context.Context, clusterID, nodeID string) (*BootstrapOperationResult, error)
}

// ClusterJoinInfo contains the secure information needed for a worker node to
// join the Kubernetes cluster. This is a highly privileged credential and must
// be protected: never logged, never exposed through public APIs, never
// persisted unnecessarily.
type ClusterJoinInfo struct {
	Endpoint  string
	Token     string
	TokenHash string
}

// ClusterStatusInfo is the authoritative Kubernetes runtime state observed
// through the Kubernetes API.
type ClusterStatusInfo struct {
	APIReachable        bool
	ServerReady         bool
	WorkerCount         int
	ReadyWorkerCount    int
	NotReadyWorkerCount int
	Version             string
	ClusterVersion      string
	Nodes               []K8sNodeInfo
	APIHealth           bool
}

// K8sNodeInfo describes a single Kubernetes node as observed through the API.
type K8sNodeInfo struct {
	Name        string
	Role        string
	Ready       bool
	Labels      map[string]string
	Annotations map[string]string
}
