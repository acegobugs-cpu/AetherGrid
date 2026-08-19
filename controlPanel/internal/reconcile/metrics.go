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

// LastReconciliation returns the wall-clock time of the most recent completed
// cycle, or the zero time when none has completed.
func (m *Metrics) LastReconciliation() time.Time {
	nanos := m.lastReconciliation.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}
