package domain

import (
	"time"
)

// NodeStatus is a strongly typed representation of a node's lifecycle status.
type NodeStatus string

// Node lifecycle statuses.
const (
	StatusUnknown       NodeStatus = "UNKNOWN"
	StatusProvisioning  NodeStatus = "PROVISIONING"
	StatusProvisioned   NodeStatus = "PROVISIONED"
	StatusBootstrapping NodeStatus = "BOOTSTRAPPING"
	StatusConnecting    NodeStatus = "CONNECTING"
	StatusRegistered    NodeStatus = "REGISTERED"
	StatusConfiguring   NodeStatus = "CONFIGURING"
	StatusReady         NodeStatus = "READY"
	StatusDegraded      NodeStatus = "DEGRADED"
	StatusUnhealthy     NodeStatus = "UNHEALTHY"
	StatusOffline       NodeStatus = "OFFLINE"
	StatusUnreachable   NodeStatus = "UNREACHABLE"
	StatusFailed        NodeStatus = "FAILED"
	StatusRecovering    NodeStatus = "RECOVERING"
	StatusRemoved       NodeStatus = "REMOVED"
)

// allStatuses is the canonical set of valid statuses.
var allStatuses = []NodeStatus{
	StatusUnknown,
	StatusProvisioning,
	StatusProvisioned,
	StatusBootstrapping,
	StatusConnecting,
	StatusRegistered,
	StatusConfiguring,
	StatusReady,
	StatusDegraded,
	StatusUnhealthy,
	StatusOffline,
	StatusUnreachable,
	StatusFailed,
	StatusRecovering,
	StatusRemoved,
}

// Valid reports whether s is a known node status.
func (s NodeStatus) Valid() bool {
	for _, candidate := range allStatuses {
		if s == candidate {
			return true
		}
	}
	return false
}

// InitialStatus is the status assigned to a node on registration.
const InitialStatus = StatusProvisioning

// DesiredInitialStatus is the desired status assigned to a node on
// registration: the operator declares that the node should eventually reach
// READY.
const DesiredInitialStatus = StatusReady

// Node is the domain model for a registered edge node.
// It is independent of both HTTP and persistence concerns.
type Node struct {
	ID                string
	Name              string
	Location          string
	IPAddress         string
	Status            NodeStatus
	DesiredStatus     NodeStatus
	KubernetesEnabled bool
	// KubernetesMinimumReadyNodes is the declared minimum Ready-node count for
	// the node's Kubernetes integration.
	KubernetesMinimumReadyNodes int
	WireGuardEnabled            bool
	LastHeartbeat               *time.Time

	// Kubernetes is the most recent Kubernetes state observed by the agent. It
	// is nil until the agent reports Kubernetes state.
	Kubernetes *KubernetesActualState

	// Role identifies the node's function in the cluster.
	Role ClusterRole

	// Reconciliation metadata, updated by the reconciliation engine.
	LastReconciliation           *time.Time
	LastSuccessfulReconciliation *time.Time
	LastReconciliationResult     ReconciliationStatus
	LastReconciliationAction     string
	LastReconciliationError      string
	LastReconciliationDeadline   *time.Time
	ReconciliationAttempts       int

	// Recovery state tracking.
	RecoveryState    RecoveryState
	RecoveryFailure  string
	RecoveryAttempts int
	LastRecoveryAt   *time.Time
	NextRetryAt      *time.Time
	// FailureStreak counts consecutive confirmed failures without an
	// intervening successful recovery; it drives flapping detection.
	FailureStreak int

	CreatedAt time.Time
	UpdatedAt time.Time
}

// DesiredState returns the structured desired state declared for the node.
func (n *Node) DesiredState() DesiredState {
	return DesiredState{
		Status: n.DesiredStatus,
		Kubernetes: KubernetesDesiredState{
			Enabled:           n.KubernetesEnabled,
			MinimumReadyNodes: n.KubernetesMinimumReadyNodes,
		},
		WireGuardEnabled: n.WireGuardEnabled,
	}
}

// ActualState returns the structured actual state currently recorded for the
// node. It reflects observation only and is never altered by desired state.
func (n *Node) ActualState() ActualState {
	return ActualState{
		Status:           n.Status,
		Kubernetes:       n.Kubernetes,
		WireGuardEnabled: n.WireGuardEnabled,
		LastHeartbeat:    n.LastHeartbeat,
	}
}
