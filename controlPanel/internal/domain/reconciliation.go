package domain

import "time"

// ReconciliationStatus is the strongly typed outcome of a reconciliation
// cycle.
type ReconciliationStatus string

// Reconciliation outcomes.
const (
	// ReconciliationInSync indicates desired and actual state match.
	ReconciliationInSync ReconciliationStatus = "IN_SYNC"
	// ReconciliationDriftDetected indicates differences exist but no
	// corrective action is applicable (for example a transitional status or a
	// detect-only run).
	ReconciliationDriftDetected ReconciliationStatus = "DRIFT_DETECTED"
	// ReconciliationReconciling indicates a corrective action was dispatched
	// and convergence is pending.
	ReconciliationReconciling ReconciliationStatus = "RECONCILING"
	// ReconciliationReconciled indicates a corrective action was executed and
	// the node has converged to the desired state.
	ReconciliationReconciled ReconciliationStatus = "RECONCILED"
	// ReconciliationFailed indicates a corrective action failed, was
	// unsupported, or timed out.
	ReconciliationFailed ReconciliationStatus = "RECONCILIATION_FAILED"
)

// ReconciliationResult is the outcome of reconciling one node.
type ReconciliationResult struct {
	NodeID       string               `json:"node_id"`
	Result       ReconciliationStatus `json:"result"`
	DesiredState DesiredState         `json:"desired_state"`
	ActualState  ActualState          `json:"actual_state"`
	Differences  []Difference         `json:"differences"`
	Action       string               `json:"action,omitempty"`
	Attempt      int                  `json:"attempt"`
	Error        string               `json:"error,omitempty"`
	// Retryable reports whether a failed cycle should be retried. It is set
	// only for retryable action failures.
	Retryable   bool      `json:"retryable,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

// ReconciliationEvent is a persisted, lightweight operational record of a
// reconciliation cycle. It is not an event-sourcing stream; current node state
// remains authoritative.
type ReconciliationEvent struct {
	ID          string               `json:"id"`
	NodeID      string               `json:"node_id"`
	StartedAt   time.Time            `json:"started_at"`
	CompletedAt time.Time            `json:"completed_at"`
	Result      ReconciliationStatus `json:"result"`
	Action      string               `json:"action,omitempty"`
	Attempt     int                  `json:"attempt"`
	Error       string               `json:"error,omitempty"`
}
