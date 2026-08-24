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
	// Recovery state for Phase 9 autonomous recovery.
	RecoveryState   RecoveryState         `json:"recovery_state"`
	RecoveryAttempt int                   `json:"recovery_attempt"`
	FailureClass    FailureClassification `json:"failure_class,omitempty"`
	NextRetryAt     *time.Time            `json:"next_retry_at,omitempty"`
	// CircuitBreaker reports whether the circuit breaker is tripped.
	CircuitBreaker bool `json:"circuit_breaker,omitempty"`
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

// AuditRecord is a Phase 9 recovery audit event describing one autonomous
// decision or action. It is stored through the same reconciliation history
// infrastructure with Result fixed to AUDIT so operators can filter it.
type AuditRecord struct {
	NodeID    string    `json:"node_id"`
	Event     string    `json:"event"`
	Detail    string    `json:"detail,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// AuditEventResult marks history rows that carry recovery audit records.
const AuditEventResult ReconciliationStatus = "AUDIT"

// AsReconciliationEvent adapts an audit record onto the persisted history row.
func (a *AuditRecord) AsReconciliationEvent() *ReconciliationEvent {
	return &ReconciliationEvent{
		NodeID:      a.NodeID,
		StartedAt:   a.Timestamp,
		CompletedAt: a.Timestamp,
		Result:      AuditEventResult,
		Action:      a.Event,
		Error:       a.Detail,
	}
}
