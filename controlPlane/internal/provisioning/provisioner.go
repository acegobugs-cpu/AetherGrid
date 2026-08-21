// Package provisioning defines the infrastructure provisioning abstraction.
// The rest of AETHER-GRID communicates with the Provisioner interface and its
// domain-shaped results rather than with Terraform internals, allowing the
// implementation to be swapped (Terraform today, Pulumi or a native provider
// later) without touching the control plane.
package provisioning

import (
	"context"

	"AetherGrid/controlPlane/internal/domain"
)

// Provisioner is the abstraction responsible for orchestrating infrastructure
// lifecycle operations: plan, apply, destroy and status.
type Provisioner interface {
	// Plan computes what would change without changing anything. It returns a
	// structured change summary; the raw textual output is provided for
	// diagnostics only.
	Plan(ctx context.Context, infra *domain.Infrastructure) (*PlanResult, error)
	// Apply converges the infrastructure to the desired state and returns the
	// resulting nodes.
	Apply(ctx context.Context, infra *domain.Infrastructure) (*ApplyResult, error)
	// Destroy removes the infrastructure. It must be invoked explicitly.
	Destroy(ctx context.Context, infra *domain.Infrastructure) error
	// Status observes the current infrastructure from authoritative state.
	Status(ctx context.Context, infra *domain.Infrastructure) (*domain.InfrastructureStatus, error)
}

// PlanResult is the structured outcome of a plan operation.
type PlanResult struct {
	Changes domain.ChangeSummary
	// Output is the raw provider output, retained for diagnostics only.
	Output string
}

// ApplyResult is the structured outcome of an apply operation.
type ApplyResult struct {
	Changes domain.ChangeSummary
	Nodes   []domain.InfrastructureNode
}
