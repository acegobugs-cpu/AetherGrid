package domain

import (
	"encoding/json"
	"time"
)

// CommandStatus is a strongly typed representation of a command's lifecycle.
type CommandStatus string

// Command lifecycle statuses.
const (
	CommandPending   CommandStatus = "PENDING"
	CommandExecuting CommandStatus = "EXECUTING"
	CommandSucceeded CommandStatus = "SUCCEEDED"
	CommandFailed    CommandStatus = "FAILED"
)

// allCommandStatuses is the canonical set of valid command statuses.
var allCommandStatuses = []CommandStatus{
	CommandPending,
	CommandExecuting,
	CommandSucceeded,
	CommandFailed,
}

// Valid reports whether s is a known command status.
func (s CommandStatus) Valid() bool {
	for _, candidate := range allCommandStatuses {
		if s == candidate {
			return true
		}
	}
	return false
}

// Terminal reports whether the command has reached a final state. Terminal
// commands are never overwritten by duplicate result reports.
func (s CommandStatus) Terminal() bool {
	return s == CommandSucceeded || s == CommandFailed
}

// Command types understood by the control plane.
const (
	// CommandGetStatus asks an agent to report its current runtime status.
	CommandGetStatus = "GET_STATUS"
	// CommandRestartAgent asks an agent to restart itself (its supervisor
	// brings it back). The reconciliation engine uses it for node recovery.
	CommandRestartAgent = "RESTART_AGENT"
	// CommandGetKubernetesStatus asks an agent to report its observed
	// Kubernetes integration state.
	CommandGetKubernetesStatus = "GET_KUBERNETES_STATUS"
	// CommandListKubernetesNodes asks an agent to list the nodes of its
	// Kubernetes cluster.
	CommandListKubernetesNodes = "LIST_KUBERNETES_NODES"
	// CommandListKubernetesPods asks an agent to list the pods of its
	// Kubernetes cluster (optionally filtered by namespace).
	CommandListKubernetesPods = "LIST_KUBERNETES_PODS"
	// CommandCreateTestNamespace asks an agent to create a dedicated test
	// namespace in its Kubernetes cluster.
	CommandCreateTestNamespace = "CREATE_TEST_NAMESPACE"
	// CommandDeleteTestNamespace asks an agent to delete a dedicated test
	// namespace from its Kubernetes cluster.
	CommandDeleteTestNamespace = "DELETE_TEST_NAMESPACE"
)

// Command is the domain model for an instruction the control plane sends to
// an edge node agent. It is independent of both HTTP and persistence concerns.
type Command struct {
	ID         string
	NodeID     string
	Type       string
	Parameters json.RawMessage
	Status     CommandStatus
	Result     json.RawMessage
	Error      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
