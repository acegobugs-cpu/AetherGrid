// Package reconcile implements the AETHER-GRID reconciliation engine. It
// observes the actual state of edge nodes, compares it against the desired
// state declared by operators, plans corrective actions for any differences and
// executes those actions through a bounded worker pool.
//
// The engine is split into the classic observer / planner / executor pattern:
//
//   - Observer: RepositoryObserver reads node state from the repository and
//     flags nodes whose heartbeats have gone stale as OFFLINE.
//   - Planner: Planner compares desired and actual state and decides which
//     action, if any, is required.
//   - Executor: Executor carries out corrective actions by dispatching
//     commands to edge agents. Actions the control plane cannot yet perform
//     fail explicitly rather than being silently ignored.
//
// Reconciler ties the three together with a periodic sweep, an event-driven
// notification channel and a worker pool that serializes work per node.
package reconcile

// Action names produced by the Planner and consumed by the Executor.
const (
	// ActionRecoverNode instructs the executor to recover an offline or
	// unhealthy node by restarting its agent.
	ActionRecoverNode = "RECOVER_NODE"
	// ActionProvisionReplacement instructs the executor to provision a
	// replacement machine for a confirmed-failed worker (Phase 9).
	ActionProvisionReplacement = "PROVISION_REPLACEMENT"
	// ActionEnableKubernetes is reserved for a later phase.
	ActionEnableKubernetes = "ENABLE_KUBERNETES"
	// ActionDisableKubernetes is reserved for a later phase.
	ActionDisableKubernetes = "DISABLE_KUBERNETES"
	// ActionEnableWireGuard is reserved for a later phase.
	ActionEnableWireGuard = "ENABLE_WIREGUARD"
	// ActionDisableWireGuard is reserved for a later phase.
	ActionDisableWireGuard = "DISABLE_WIREGUARD"
)

// Error messages surfaced to operators.
const (
	// ErrTextUnsupportedAction explains that an action is recognized but the
	// control plane cannot execute it yet.
	ErrTextUnsupportedAction = "action is not yet supported by the executor"
	// ErrTextNodeNotFound is reported when a node disappears mid-cycle.
	ErrTextNodeNotFound = "node not found"
	// ErrTextAgentRestartFailed is reported when dispatching the recovery
	// command failed.
	ErrTextAgentRestartFailed = "failed to dispatch agent restart"
)

// UnsupportedActionError is returned by the executor when a planned action has
// no execution path yet. It is not retryable: the difference it targets cannot
// be corrected until a later phase ships the capability.
type UnsupportedActionError struct {
	Action string
}

// Error implements error.
func (e *UnsupportedActionError) Error() string {
	return ErrTextUnsupportedAction + ": " + e.Action
}

// RetryableError wraps a transient execution failure that may be retried.
// UnsupportedActionError is never wrapped in RetryableError.
type RetryableError struct {
	Err error
}

// Error implements error.
func (e *RetryableError) Error() string {
	return e.Err.Error()
}

// Unwrap exposes the wrapped error.
func (e *RetryableError) Unwrap() error {
	return e.Err
}
