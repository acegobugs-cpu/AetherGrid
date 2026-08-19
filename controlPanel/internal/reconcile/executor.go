package reconcile

import (
	"context"
	"errors"
	"fmt"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
	"github.com/acegobugs-cpu/AetherGrid/internal/repository"
)

// CommandDispatcher is the subset of the command service the executor needs.
// It is an interface so tests can drive the executor without a real service.
type CommandDispatcher interface {
	// DispatchRestart enqueues a RESTART_AGENT command for the node and
	// returns it. It returns an error when the node no longer exists.
	DispatchRestart(ctx context.Context, nodeID string) (*domain.Command, error)
}

// Executor is the execution abstraction. It carries out the corrective actions
// planned by the planner.
type Executor interface {
	// Execute applies the planned action. It returns a wrapped error whose
	// retryability the engine inspects via errors.As + RetryableError.
	Execute(ctx context.Context, plan Plan) error
}

// ReconciliationExecutor executes planned corrective actions against the
// command queue. Today only RECOVER_NODE has an execution path: it dispatches
// a RESTART_AGENT command to the edge agent. All other actions are rejected as
// unsupported so a later phase must add support explicitly.
type ReconciliationExecutor struct {
	commands CommandDispatcher
}

// NewReconciliationExecutor constructs an executor that dispatches recovery
// commands through the given dispatcher.
func NewReconciliationExecutor(commands CommandDispatcher) *ReconciliationExecutor {
	return &ReconciliationExecutor{commands: commands}
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
	case ActionEnableKubernetes,
		ActionDisableKubernetes,
		ActionEnableWireGuard,
		ActionDisableWireGuard:
		return &UnsupportedActionError{Action: plan.Action}
	default:
		return &UnsupportedActionError{Action: plan.Action}
	}
}
