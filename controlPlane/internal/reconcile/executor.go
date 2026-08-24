package reconcile

import (
	"context"
	"errors"
	"fmt"

	"AetherGrid/controlPlane/internal/domain"
	"AetherGrid/controlPlane/internal/repository"
)

// CommandDispatcher is the subset of the command service the executor needs.
// It is an interface so tests can drive the executor without a real service.
type CommandDispatcher interface {
	// DispatchRestart enqueues a RESTART_AGENT command for the node and
	// returns it. It returns an error when the node no longer exists.
	DispatchRestart(ctx context.Context, nodeID string) (*domain.Command, error)
}

// ReplacementProvisioner provisions replacement machines for confirmed-failed
// worker nodes. It is optional: when nil, PROVISION_REPLACEMENT actions fail
// explicitly as unsupported rather than being silently ignored. Implementations
// must reuse the Phase 6 provisioning layer; they must never shell out to
// infrastructure tooling directly.
type ReplacementProvisioner interface {
	// ProvisionReplacement provisions and registers a replacement for the
	// failed node and returns the new node.
	ProvisionReplacement(ctx context.Context, failed *domain.Node) (*domain.Node, error)
}

// Executor is the execution abstraction. It carries out the corrective actions
// planned by the planner.
type Executor interface {
	// Execute applies the planned action. It returns a wrapped error whose
	// retryability the engine inspects via errors.As + RetryableError.
	Execute(ctx context.Context, plan Plan) error
}

// ReconciliationExecutor executes planned corrective actions against the
// command queue. RECOVER_NODE dispatches a RESTART_AGENT command to the edge
// agent; PROVISION_REPLACEMENT delegates to the optional replacement
// provisioner. All other actions are rejected as unsupported so a later phase
// must add support explicitly.
type ReconciliationExecutor struct {
	commands    CommandDispatcher
	replacement ReplacementProvisioner
	nodes       NodeRepository
}

// NewReconciliationExecutor constructs an executor that dispatches recovery
// commands through the given dispatcher. The replacement provisioner and node
// repository are optional (pass nil).
func NewReconciliationExecutor(
	commands CommandDispatcher,
	replacement ReplacementProvisioner,
	nodes NodeRepository,
) *ReconciliationExecutor {
	return &ReconciliationExecutor{
		commands:    commands,
		replacement: replacement,
		nodes:       nodes,
	}
}

// Execute applies a planned action.
func (e *ReconciliationExecutor) Execute(ctx context.Context, plan Plan) error {
	switch plan.Action {
	case ActionRecoverNode:
		if _, err := e.commands.DispatchRestart(ctx, plan.NodeID); err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				// The node is gone; retrying a dispatch cannot help.
				return fmt.Errorf("%s: %w", ErrTextAgentRestartFailed, err)
			}
			return &RetryableError{Err: fmt.Errorf("%s: %w", ErrTextAgentRestartFailed, err)}
		}
		return nil
	case ActionProvisionReplacement:
		if e.replacement == nil || e.nodes == nil {
			return &UnsupportedActionError{Action: plan.Action}
		}
		failed, err := e.nodes.GetByID(ctx, plan.NodeID)
		if err != nil {
			return fmt.Errorf("%s: %w", "failed to load failed node", err)
		}
		if _, err := e.replacement.ProvisionReplacement(ctx, failed); err != nil {
			// Provisioning failures are usually transient (provider API,
			// network); mark retryable so bounded retries apply.
			return &RetryableError{Err: fmt.Errorf("provisioning replacement: %w", err)}
		}
		return nil
	case ActionEnableKubernetes,
		ActionDisableKubernetes,
		ActionEnableWireGuard,
		ActionDisableWireGuard:
		return &UnsupportedActionError{Action: plan.Action}
	default:
		return &UnsupportedActionError{Action: plan.Action}
	}
}
