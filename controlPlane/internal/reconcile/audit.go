package reconcile

import (
	"context"

	"AetherGrid/controlPlane/internal/domain"
)

// Recovery audit event names (Phase 9 #60). They are recorded through the
// existing reconciliation history infrastructure so operators can reconstruct
// every autonomous decision: the observed failure, the desired state, the
// selected action, the governing policy and the result.
const (
	// AuditNodeFailureDetected is emitted when a node first shows failure
	// evidence and enters the SUSPECTED state.
	AuditNodeFailureDetected = "NODE_FAILURE_DETECTED"
	// AuditNodeFailureConfirmed is emitted when sustained evidence crosses
	// the configured confirmation threshold.
	AuditNodeFailureConfirmed = "NODE_FAILURE_CONFIRMED"
	// AuditRecoveryStarted is emitted when a recovery workflow dispatches its
	// first action for a node.
	AuditRecoveryStarted = "RECOVERY_STARTED"
	// AuditRecoveryAttemptFailed is emitted each time a recovery action fails
	// and a retry is scheduled.
	AuditRecoveryAttemptFailed = "RECOVERY_ATTEMPT_FAILED"
	// AuditReplacementProvisioned is emitted after a replacement machine was
	// provisioned for a confirmed-failed worker.
	AuditReplacementProvisioned = "REPLACEMENT_PROVISIONED"
	// AuditNodeRejoined is emitted after a recovered node rejoins and reports
	// healthy again.
	AuditNodeRejoined = "NODE_REJOINED"
	// AuditRecoveryCompleted is emitted once verification confirms the
	// desired state is satisfied again.
	AuditRecoveryCompleted = "RECOVERY_COMPLETED"
	// AuditRecoveryBlocked is emitted when policy, circuit breaker or
	// preconditions forbid a recovery.
	AuditRecoveryBlocked = "RECOVERY_BLOCKED"
)

// auditRecorder writes one audit row through the existing history hook. The
// node ID, the event name and an explanation are preserved; secrets are never
// included in audit output.
func (r *Reconciler) audit(ctx context.Context, nodeID, event, detail string) {
	if r.history == nil {
		return
	}
	now := r.now().UTC()
	row := &domain.AuditRecord{
		NodeID:    nodeID,
		Event:     event,
		Detail:    detail,
		Timestamp: now,
	}
	if err := r.history(ctx, row.AsReconciliationEvent()); err != nil {
		r.logger.Printf("persisting audit event node=%s event=%s error=%v", nodeID, event, err)
	}
}
