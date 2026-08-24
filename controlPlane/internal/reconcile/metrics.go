package reconcile

import (
	"sync/atomic"
	"time"
)

// Metrics exposes the health of the reconciliation engine. It is safe for
// concurrent use and is only for observability; the engine never makes control
// decisions based on it.
type Metrics struct {
	// NodesReconciled is the number of completed cycles.
	NodesReconciled atomic.Int64
	// CyclesInSync counts cycles that found no differences.
	CyclesInSync atomic.Int64
	// CyclesDrifted counts cycles that found drift.
	CyclesDrifted atomic.Int64
	// CyclesReconciled counts cycles that converged a drifted node.
	CyclesReconciled atomic.Int64
	// CyclesFailed counts cycles that failed to reconcile.
	CyclesFailed atomic.Int64
	// CommandsDispatched counts corrective commands sent to agents.
	CommandsDispatched atomic.Int64

	// Phase 9 recovery counters.
	// NodeFailures counts confirmed node failures.
	NodeFailures atomic.Int64
	// RecoveriesStarted counts recovery workflows started.
	RecoveriesStarted atomic.Int64
	// RecoveryAttempts counts individual recovery attempts.
	RecoveryAttempts atomic.Int64
	// RecoveriesSucceeded counts completed recoveries.
	RecoveriesSucceeded atomic.Int64
	// RecoveriesFailed counts exhausted recoveries.
	RecoveriesFailed atomic.Int64
	// RecoveriesBlocked counts circuit-breaker trips.
	RecoveriesBlocked atomic.Int64
	// NodesSuspected counts nodes marked suspected (unreachable).
	NodesSuspected atomic.Int64
	// NodesRecoveredFromSuspicion counts transient failures that healed.
	NodesRecoveredFromSuspicion atomic.Int64
	// PendingWork is the number of nodes awaiting reconciliation. Reads are
	// best-effort snapshots of the queue.
	PendingWork func() int
	// LastReconciliation is the wall-clock time of the most recent completed
	// cycle.
	lastReconciliation atomic.Int64
}

func newMetrics(pending func() int) *Metrics {
	return &Metrics{PendingWork: pending}
}

// recordCycle updates the metrics for one completed cycle.
func (m *Metrics) recordCycle(started time.Time, result string) {
	m.NodesReconciled.Add(1)
	m.lastReconciliation.Store(started.UnixNano())

	switch result {
	case "IN_SYNC":
		m.CyclesInSync.Add(1)
	case "DRIFT_DETECTED":
		m.CyclesDrifted.Add(1)
	case "RECONCILED":
		m.CyclesReconciled.Add(1)
	case "RECONCILIATION_FAILED":
		m.CyclesFailed.Add(1)
	}
}

// recordCommand increments the dispatched-command counter.
func (m *Metrics) recordCommand() {
	m.CommandsDispatched.Add(1)
}

// recordNodeFailure increments the confirmed-failure counter.
func (m *Metrics) recordNodeFailure() {
	m.NodeFailures.Add(1)
}

// recordRecoveryStarted increments the recovery-workflow counter.
func (m *Metrics) recordRecoveryStarted() {
	m.RecoveriesStarted.Add(1)
}

// recordRecoveryAttempt increments the per-attempt counter.
func (m *Metrics) recordRecoveryAttempt() {
	m.RecoveryAttempts.Add(1)
}

// recordRecoverySuccess increments the successful-recovery counter.
func (m *Metrics) recordRecoverySuccess() {
	m.RecoveriesSucceeded.Add(1)
}

// recordRecoveryFailure increments the exhausted-recovery counter.
func (m *Metrics) recordRecoveryFailure() {
	m.RecoveriesFailed.Add(1)
}

// recordRecoveryBlocked increments the circuit-breaker counter.
func (m *Metrics) recordRecoveryBlocked() {
	m.RecoveriesBlocked.Add(1)
}

// recordSuspicion increments the suspected-node counter.
func (m *Metrics) recordSuspicion() {
	m.NodesSuspected.Add(1)
}

// recordSuspicionCleared counts a transient failure that healed before
// reaching the failure threshold.
func (m *Metrics) recordSuspicionCleared() {
	m.NodesRecoveredFromSuspicion.Add(1)
}

// LastReconciliation returns the wall-clock time of the most recent completed
// cycle, or the zero time when none has completed.
func (m *Metrics) LastReconciliation() time.Time {
	nanos := m.lastReconciliation.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}
