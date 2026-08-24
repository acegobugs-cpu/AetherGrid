package domain

import (
	"errors"
	"strings"
	"time"
)

// Cluster operation types.
type ClusterOperationType string

const (
	ClusterOpBootstrap ClusterOperationType = "BOOTSTRAP"
	ClusterOpRemove    ClusterOperationType = "REMOVE"
)

// Cluster operation statuses.
type ClusterOperationStatus string

const (
	ClusterOpPending   ClusterOperationStatus = "PENDING"
	ClusterOpRunning   ClusterOperationStatus = "RUNNING"
	ClusterOpSucceeded ClusterOperationStatus = "SUCCEEDED"
	ClusterOpFailed    ClusterOperationStatus = "FAILED"
	ClusterOpCancelled ClusterOperationStatus = "CANCELLED"
)

// allClusterOpStatuses is the canonical set of valid operation statuses.
var allClusterOpStatuses = []ClusterOperationStatus{
	ClusterOpPending,
	ClusterOpRunning,
	ClusterOpSucceeded,
	ClusterOpFailed,
	ClusterOpCancelled,
}

// Valid reports whether s is a known operation status.
func (s ClusterOperationStatus) Valid() bool {
	for _, candidate := range allClusterOpStatuses {
		if s == candidate {
			return true
		}
	}
	return false
}

// Terminal reports whether the operation has reached a final state.
func (s ClusterOperationStatus) Terminal() bool {
	return s == ClusterOpSucceeded || s == ClusterOpFailed || s == ClusterOpCancelled
}

// Kubernetes error types for bootstrap failures.
var (
	ErrKubernetesInstallationFailed     = errors.New("kubernetes installation failed")
	ErrControlPlaneInitializationFailed = errors.New("control-plane initialization failed")
	ErrKubernetesAPITimeout             = errors.New("kubernetes API timeout")
	ErrWorkerJoinFailed                 = errors.New("worker join failed")
	ErrKubernetesNodeNotReady           = errors.New("kubernetes node not ready")
	ErrVersionMismatch                  = errors.New("version mismatch")
	ErrClusterVerificationFailed        = errors.New("cluster verification failed")
	ErrInvalidClusterToken              = errors.New("invalid cluster token")
	ErrNodeAlreadyInCluster             = errors.New("node already in cluster")
	ErrClusterOperationInProgress       = errors.New("cluster operation already in progress")
	ErrClusterNotFound                  = errors.New("cluster not found")
	ErrNodeNotReadyForBootstrap         = errors.New("node not ready for kubernetes bootstrap")
	ErrInvalidJoinInfo                  = errors.New("invalid join information retrieved")
)

// Kubernetes label constants used by AETHER-GRID to associate Kubernetes
// nodes with AETHER-GRID nodes. These are centralized so label names are
// never scattered as string literals throughout the codebase.
const (
	LabelNodeID     = "aether-grid/node-id"
	LabelClusterID  = "aether-grid/cluster-id"
	LabelRole       = "aether-grid/role"
	LabelK3sVersion = "aether-grid/k3s-version"
)

// ClusterRole identifies the functional role of a Kubernetes node.
type ClusterRole string

// Kubernetes node roles.
const (
	RoleControlPlane ClusterRole = "ControlPlane"
	RoleWorker       ClusterRole = "Worker"
)

// allClusterRoles is the canonical set of valid roles.
var allClusterRoles = []ClusterRole{
	RoleControlPlane,
	RoleWorker,
}

// Valid reports whether r is a known cluster role.
func (r ClusterRole) Valid() bool {
	for _, candidate := range allClusterRoles {
		if r == candidate {
			return true
		}
	}
	return false
}

// ClusterLifecycleState represents the lifecycle state of a Kubernetes cluster
// managed by AETHER-GRID. Unlike a simple boolean, this state machine makes
// partial success and degradation explicit.
type ClusterLifecycleState string

// Cluster lifecycle states.
const (
	ClusterStatePending       ClusterLifecycleState = "PENDING"
	ClusterStateBootstrapping ClusterLifecycleState = "BOOTSTRAPPING"
	ClusterStateCPReady       ClusterLifecycleState = "CONTROL_PLANE_READY"
	ClusterStateJoining       ClusterLifecycleState = "JOINING_WORKERS"
	ClusterStateVerifying     ClusterLifecycleState = "VERIFYING"
	ClusterStateReady         ClusterLifecycleState = "READY"
	ClusterStateDegraded      ClusterLifecycleState = "DEGRADED"
	ClusterStateRecovering    ClusterLifecycleState = "RECOVERING"
	ClusterStateFailed        ClusterLifecycleState = "FAILED"
	ClusterStateDestroyed     ClusterLifecycleState = "DESTROYED"
)

// allClusterStates is the canonical set of valid cluster lifecycle states.
var allClusterStates = []ClusterLifecycleState{
	ClusterStatePending,
	ClusterStateBootstrapping,
	ClusterStateCPReady,
	ClusterStateJoining,
	ClusterStateVerifying,
	ClusterStateReady,
	ClusterStateDegraded,
	ClusterStateRecovering,
	ClusterStateFailed,
	ClusterStateDestroyed,
}

// Valid reports whether s is a known cluster lifecycle state.
func (s ClusterLifecycleState) Valid() bool {
	for _, candidate := range allClusterStates {
		if s == candidate {
			return true
		}
	}
	return false
}

// Terminal reports whether the cluster state is a final state. A cluster in a
// terminal state is not executing an operation.
func (s ClusterLifecycleState) Terminal() bool {
	return s == ClusterStateReady || s == ClusterStateFailed || s == ClusterStateDestroyed
}

// IsReady reports whether the cluster has reached the Ready state.
func (s ClusterLifecycleState) IsReady() bool {
	return s == ClusterStateReady
}

// IsDegraded reports whether the cluster is in a degraded but recoverable state.
func (s ClusterLifecycleState) IsDegraded() bool {
	return s == ClusterStateDegraded
}

// Cluster validation sentinels.
var (
	ErrClusterEmptyName        = errors.New("cluster name is required")
	ErrClusterNameTooLong      = errors.New("cluster name must be at most 63 characters")
	ErrClusterNameInvalidChars = errors.New("cluster name must not contain spaces or slashes")
	ErrClusterNoVersion        = errors.New("kubernetes version is required")
	ErrClusterNoCPNode         = errors.New("control-plane node is required")
	ErrClusterNoWorkers        = errors.New("at least one worker node is required")
	ErrClusterNotCPNode        = errors.New("control-plane node must have role ControlPlane")
	ErrClusterNotWorkerNode    = errors.New("worker node must have role Worker")
)

// ClusterSpec is the declarative desired state of a Kubernetes cluster.
type ClusterSpec struct {
	Name              string
	K3sVersion        string
	ControlPlaneNode  string
	WorkerNodes       []string
	WorkerConcurrency int
}

// Validate enforces the rules a ClusterSpec must satisfy.
func (s ClusterSpec) Validate() error {
	if len(s.Name) == 0 {
		return ErrClusterEmptyName
	}
	if len(s.Name) > 63 {
		return ErrClusterNameTooLong
	}
	if strings.ContainsAny(s.Name, " /\\") {
		return ErrClusterNameInvalidChars
	}
	if len(s.K3sVersion) == 0 {
		return ErrClusterNoVersion
	}
	if len(s.ControlPlaneNode) == 0 {
		return ErrClusterNoCPNode
	}
	if len(s.WorkerNodes) == 0 {
		return ErrClusterNoWorkers
	}
	if s.WorkerConcurrency < 1 {
		s.WorkerConcurrency = 3
	}
	return nil
}

// ClusterNode describes a Kubernetes node within a cluster from AETHER-GRID's
// perspective. It associates the AETHER-GRID node with its Kubernetes identity.
type ClusterNode struct {
	NodeID      string
	Role        ClusterRole
	K8sNodeName string
	Ready       bool
	Labels      map[string]string
}

// ClusterStatus is the observed state of a Kubernetes cluster.
type ClusterStatus struct {
	State             ClusterLifecycleState
	KubernetesVersion string
	ControlPlaneNode  string
	WorkerNodes       []ClusterNode
	ReadyWorkerCount  int
	APIEndpoint       string
	LastOperation     string
	LastError         string
	LastVerifiedAt    *time.Time
}

// Cluster is the aggregate root for a Kubernetes cluster managed by AETHER-GRID.
// It is independent of HTTP and persistence concerns.
type Cluster struct {
	ID        string
	Spec      ClusterSpec
	Status    ClusterStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ClusterOperation is a long-running cluster bootstrap operation tracked for
// asynchronous execution and resumption.
type ClusterOperation struct {
	ID             string
	ClusterID      string
	Type           ClusterOperationType
	Status         ClusterOperationStatus
	StartedAt      *time.Time
	CompletedAt    *time.Time
	Error          string
	CurrentStep    string
	SucceededSteps []string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// KubernetesBootstrapStep is an individual step in the Kubernetes bootstrap process.
type KubernetesBootstrapStep string

// Kubernetes bootstrap steps.
const (
	K8sBootstrapStepValidateCluster   KubernetesBootstrapStep = "VALIDATE_CLUSTER"
	K8sBootstrapStepValidateCPNode    KubernetesBootstrapStep = "VALIDATE_CP_NODE"
	K8sBootstrapStepVerifyCPNetwork   KubernetesBootstrapStep = "VERIFY_CP_NETWORK"
	K8sBootstrapStepInstallServer     KubernetesBootstrapStep = "INSTALL_SERVER"
	K8sBootstrapStepInitializeServer  KubernetesBootstrapStep = "INITIALIZE_SERVER"
	K8sBootstrapStepWaitForAPI        KubernetesBootstrapStep = "WAIT_FOR_API"
	K8sBootstrapStepRetrieveJoinInfo  KubernetesBootstrapStep = "RETRIEVE_JOIN_INFO"
	K8sBootstrapStepVerifyServerReady KubernetesBootstrapStep = "VERIFY_SERVER_READY"
	K8sBootstrapStepBootstrapWorkers  KubernetesBootstrapStep = "BOOTSTRAP_WORKERS"
	K8sBootstrapStepJoinWorkers       KubernetesBootstrapStep = "JOIN_WORKERS"
	K8sBootstrapStepWaitForWorkers    KubernetesBootstrapStep = "WAIT_FOR_WORKERS"
	K8sBootstrapStepVerifyCluster     KubernetesBootstrapStep = "VERIFY_CLUSTER"
	K8sBootstrapStepRegisterCluster   KubernetesBootstrapStep = "REGISTER_CLUSTER"
)

// ReadyCount returns the number of ready worker nodes.
func (c *Cluster) ReadyCount() int {
	count := 0
	for _, w := range c.Status.WorkerNodes {
		if w.Ready {
			count++
		}
	}
	return count
}

// DesiredWorkerCount returns the declared number of worker nodes.
func (c *Cluster) DesiredWorkerCount() int {
	return len(c.Spec.WorkerNodes)
}

// IsHealthy reports whether the cluster has all expected workers ready.
func (c *Cluster) IsHealthy() bool {
	return c.Status.State == ClusterStateReady
}
