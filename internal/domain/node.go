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
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
