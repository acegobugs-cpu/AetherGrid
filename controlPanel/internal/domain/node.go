package domain

import (
	"time"
)

// NodeStatus is a strongly typed representation of a node's lifecycle status.
type NodeStatus string

// Node lifecycle statuses.
const (
	StatusProvisioning NodeStatus = "PROVISIONING"
	StatusProvisioned  NodeStatus = "PROVISIONED"
	StatusConnecting   NodeStatus = "CONNECTING"
	StatusRegistered   NodeStatus = "REGISTERED"
	StatusConfiguring  NodeStatus = "CONFIGURING"
	StatusReady        NodeStatus = "READY"
	StatusUnhealthy    NodeStatus = "UNHEALTHY"
	StatusOffline      NodeStatus = "OFFLINE"
	StatusRecovering   NodeStatus = "RECOVERING"
)

// allStatuses is the canonical set of valid statuses.
var allStatuses = []NodeStatus{
	StatusProvisioning,
	StatusProvisioned,
	StatusConnecting,
	StatusRegistered,
	StatusConfiguring,
	StatusReady,
	StatusUnhealthy,
	StatusOffline,
	StatusRecovering,
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
	WireGuardEnabled  bool
	LastHeartbeat     *time.Time

	// Reconciliation metadata, updated by the reconciliation engine.
	LastReconciliation           *time.Time
	LastSuccessfulReconciliation *time.Time
	LastReconciliationResult     ReconciliationStatus
	LastReconciliationAction     string
	LastReconciliationError      string
	LastReconciliationDeadline   *time.Time
	ReconciliationAttempts       int

	CreatedAt time.Time
	UpdatedAt time.Time
}

// DesiredState returns the structured desired state declared for the node.
func (n *Node) DesiredState() DesiredState {
	return DesiredState{
		Status:            n.DesiredStatus,
		KubernetesEnabled: n.KubernetesEnabled,
		WireGuardEnabled:  n.WireGuardEnabled,
	}
}

// ActualState returns the structured actual state currently recorded for the
// node. It reflects observation only and is never altered by desired state.
func (n *Node) ActualState() ActualState {
	return ActualState{
		Status:            n.Status,
		KubernetesEnabled: n.KubernetesEnabled,
		WireGuardEnabled:  n.WireGuardEnabled,
		LastHeartbeat:     n.LastHeartbeat,
	}
}
