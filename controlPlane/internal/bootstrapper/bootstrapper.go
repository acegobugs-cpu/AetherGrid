package bootstrapper

import (
	"context"
	"time"

	"AetherGrid/controlPlane/internal/domain"
)

// BootstrapStatus is the status of a bootstrap operation.
type BootstrapStatus string

// Bootstrap statuses.
const (
	BootstrapStatusPending   BootstrapStatus = "PENDING"
	BootstrapStatusRunning   BootstrapStatus = "RUNNING"
	BootstrapStatusSucceeded BootstrapStatus = "SUCCEEDED"
	BootstrapStatusFailed    BootstrapStatus = "FAILED"
	BootstrapStatusCancelled BootstrapStatus = "CANCELLED"
)

// BootstrapStep is an individual step in the bootstrap process.
type BootstrapStep string

// Bootstrap steps.
const (
	BootstrapStepPrepare      BootstrapStep = "PREPARE"
	BootstrapStepConnect      BootstrapStep = "CONNECT"
	BootstrapStepPrepareOS    BootstrapStep = "PREPARE_OS"
	BootstrapStepConfigureNet BootstrapStep = "CONFIGURE_NET"
	BootstrapStepInstallAgent BootstrapStep = "INSTALL_AGENT"
	BootstrapStepVerify       BootstrapStep = "VERIFY"
	BootstrapStepRegister     BootstrapStep = "REGISTER"
	BootstrapStepComplete     BootstrapStep = "COMPLETE"
)

// Bootstrap represents a node bootstrap operation with a state machine.
// Bootstrap is long-running and must be resumable after failures or restarts.
type Bootstrap interface {
	// Bootstrap executes the full bootstrap sequence for a node.
	// It returns immediately; the caller should use GetOperation to track progress.
	Bootstrap(ctx context.Context, nodeID string) (*domain.InfrastructureOperation, error)

	// GetOperation returns the current status of a bootstrap operation.
	GetOperation(ctx context.Context, operationID string) (*domain.InfrastructureOperation, error)

	// CancelOperation cancels an in-flight bootstrap operation.
	CancelOperation(ctx context.Context, operationID string) error

	// Status returns the current bootstrap state machine state for a node.
	Status(ctx context.Context, nodeID string) (domain.BootstrapPhase, error)
}

// BootstrapOperationResult is the result of a single bootstrap step.
type BootstrapOperationResult struct {
	Step        BootstrapStep
	Succeeded   bool
	Error       string
	CompletedAt time.Time
}

// NewBootstrapOperationResult creates a new BootstrapOperationResult.
func NewBootstrapOperationResult(step BootstrapStep, succeeded bool, errorMsg string) BootstrapOperationResult {
	return BootstrapOperationResult{
		Step:        step,
		Succeeded:   succeeded,
		Error:       errorMsg,
		CompletedAt: time.Now().UTC(),
	}
}

// Bootstrapper is the abstraction responsible for bootstrapping a newly
// provisioned edge node. It coordinates OS preparation, WireGuard
// configuration, Edge Agent installation, and registration with the
// control plane.
//
// The exact implementation should follow existing project conventions; the
// initial implementation is SSHBootstrapper.
type Bootstrapper interface {
	// Prepare prepares the target node for bootstrap: verifies OS, architecture,
	// resources, and network connectivity.
	Prepare(ctx context.Context, nodeID string) (*BootstrapOperationResult, error)

	// InstallAgent transfers and installs the Edge Agent on the node, configures
	// the systemd service, and starts the agent.
	InstallAgent(ctx context.Context, nodeID string) (*BootstrapOperationResult, error)

	// ConfigureNetwork configures WireGuard on the node: generates keys, sets up
	// the peer, and verifies private network connectivity.
	ConfigureNetwork(ctx context.Context, nodeID string) (*BootstrapOperationResult, error)

	// Verify checks that the node is operational: SSH is responding, WireGuard
	// handshake is confirmed, the agent is running, and the node can reach the
	// control plane.
	Verify(ctx context.Context, nodeID string) (*BootstrapOperationResult, error)

	// Bootstrap executes the full bootstrap sequence: Prepare → ConfigureNetwork →
	// InstallAgent → Verify → Register. Each step is idempotent and resumable.
	Bootstrap(ctx context.Context, nodeID string) (*BootstrapOperationResult, error)
}

// BootstrapOperation is a recorded bootstrap operation for tracking and
// resumption. It persists the step-by-step progress of bootstrap so that
// after a control-plane restart, bootstrap can continue from where it left off.
type BootstrapOperation struct {
	ID          string
	NodeID      string
	CurrentStep BootstrapStep
	Status      BootstrapStatus
	StartedAt   *time.Time
	CompletedAt *time.Time
	Error       string
	CreatedAt   time.Time
}

// NewBootstrapOperation creates a new BootstrapOperation with the given ID and
// node ID, starting at the Pending step.
func NewBootstrapOperation(id, nodeID string) BootstrapOperation {
	return BootstrapOperation{
		ID:          id,
		NodeID:      nodeID,
		CurrentStep: BootstrapStepPrepare,
		Status:      BootstrapStatusPending,
		CreatedAt:   time.Now().UTC(),
	}
}
