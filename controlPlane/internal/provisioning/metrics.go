package provisioning

import (
	"sync/atomic"
	"time"
)

// Metrics exposes infrastructure operation counters. It is safe for concurrent
// use and is only for observability; it never influences control decisions.
type Metrics struct {
	// OperationsTotal counts every started operation.
	OperationsTotal atomic.Int64
	// OperationFailures counts operations that failed or were cancelled.
	OperationFailures atomic.Int64
	// OperationsRunning is a best-effort gauge of in-flight operations.
	OperationsRunning atomic.Int64
	// LastOperationDurationNanos is the duration of the most recent operation.
	lastOperationDurationNanos atomic.Int64
}

// record updates the metrics for one completed operation.
func (m *Metrics) record(started time.Time, failed bool) {
	m.OperationsTotal.Add(1)
	m.OperationsRunning.Add(-1)
	if failed {
		m.OperationFailures.Add(1)
	}
	m.lastOperationDurationNanos.Store(time.Since(started).Nanoseconds())
}

// OperationStarted registers a starting operation and returns a function that
// must be called exactly once when the operation completes.
func (m *Metrics) OperationStarted() func(failed bool) {
	m.OperationsRunning.Add(1)
	started := time.Now()
	return func(failed bool) {
		m.record(started, failed)
	}
}

// LastOperationDuration returns the duration of the most recent completed
// operation, or zero when none has completed.
func (m *Metrics) LastOperationDuration() time.Duration {
	return time.Duration(m.lastOperationDurationNanos.Load())
}
