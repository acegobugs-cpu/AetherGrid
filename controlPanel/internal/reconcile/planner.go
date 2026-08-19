package reconcile

import (
	"context"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
)

// Plan is the planner's decision for one node.
type Plan struct {
	// NodeID identifies the node being planned.
	NodeID string
	// Desired is the desired state used for the decision.
	Desired domain.DesiredState
	// Actual is the actual state used for the decision.
	Actual domain.ActualState
	// Differences are the structured differences between desired and actual.
	Differences []domain.Difference
	// Action is the corrective action to take, if any.
	Action string
}

// NeedsAction reports whether the plan carries a corrective action.
func (p Plan) NeedsAction() bool {
	return p.Action != ""
}

// Planner is the planning abstraction. Given a strategy it decides whether a
// corrective action is required and, if so, which one.
type Planner interface {
	// Plan decides the action for one node.
	Plan(ctx context.Context, strategy Strategy) (Plan, error)
}

// ReconciliationPlanner implements the Planner. It turns structured state
// differences into corrective actions:
//
//   - An OFFLINE or UNHEALTHY status difference means the node needs recovery,
//     so the action is RECOVER_NODE.
//   - A difference involving a transitional status (for example PROVISIONING
//     while desired is READY) is expected mid-lifecycle and produces no action;
//     the plan still reports DRIFT_DETECTED so operators can see it.
//   - Kubernetes differences (kubernetes.available, kubernetes.ready_nodes)
//     indicate the cluster is not meeting the declared expectation. There is no
//     executable remediation path in Phase 4, so no action is planned and the
//     difference is surfaced as DRIFT_DETECTED.
//   - WireGuard differences are recognized but cannot be acted on yet; the
//     executor rejects them explicitly.
type ReconciliationPlanner struct{}

// NewReconciliationPlanner constructs a planner.
func NewReconciliationPlanner() *ReconciliationPlanner {
	return &ReconciliationPlanner{}
}

// Plan decides the corrective action for a node.
func (p *ReconciliationPlanner) Plan(ctx context.Context, strategy Strategy) (Plan, error) {
	differences := domain.CompareStates(strategy.Desired, strategy.Actual)

	// A stale heartbeat means the node is effectively offline regardless of its
	// stored status. When the stored status already matches desired, staleness
	// is the only evidence of drift, so it must be surfaced as a difference;
	// otherwise the node would appear in sync.
	if strategy.Stale && strategy.Actual.Status == strategy.Desired.Status {
		differences = append(differences, domain.Difference{
			Field:   domain.FieldStatus,
			Desired: strategy.Desired.Status,
			Actual:  domain.StatusOffline,
		})
	}

	plan := Plan{
		NodeID:      strategy.NodeID,
		Desired:     strategy.Desired,
		Actual:      strategy.Actual,
		Differences: differences,
	}

	if len(differences) == 0 {
		return plan, nil
	}

	// Unhealthy and offline nodes need recovery before anything else. A stale
	// heartbeat makes the effective status OFFLINE.
	effectiveStatus := strategy.Actual.Status
	if strategy.Stale {
		effectiveStatus = domain.StatusOffline
	}

	if effectiveStatus == domain.StatusOffline || effectiveStatus == domain.StatusUnhealthy {
		plan.Action = ActionRecoverNode
		return plan, nil
	}

	// Transitional statuses (PROVISIONING, CONNECTING, CONFIGURING,
	// RECOVERING) indicate an in-flight lifecycle step, not an error.
	if transitionalStatus(effectiveStatus) {
		return plan, nil
	}

	// Remaining differences: status transition mismatch (for example REGISTERED
	// when READY is desired) or an infrastructure flag difference. These are
	// recognized but have no executable path yet, so no action is planned.
	return plan, nil
}

// transitionalStatus reports whether the status represents a lifecycle step
// that is expected to resolve on its own.
func transitionalStatus(status domain.NodeStatus) bool {
	switch status {
	case domain.StatusProvisioning,
		domain.StatusConnecting,
		domain.StatusConfiguring,
		domain.StatusRecovering:
		return true
	default:
		return false
	}
}
