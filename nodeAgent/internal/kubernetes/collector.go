package kubernetes

import "context"

// Collector gathers the observed Kubernetes state for inclusion in the agent's
// actual-state report. It is separate from the Linux node-state collector so a
// Kubernetes failure can never break basic agent state collection.
type Collector interface {
	// Collect returns the observed Kubernetes state. It never returns a hard
	// error: an unreachable or disabled cluster is reported as a state.
	Collect(ctx context.Context) KubernetesState
}

// StateCollector is the production KubernetesStateCollector. It is safe for
// concurrent use.
type StateCollector struct {
	service *Service
}

// NewStateCollector constructs a collector backed by the given service.
func NewStateCollector(service *Service) *StateCollector {
	return &StateCollector{service: service}
}

// Collect returns the observed Kubernetes state, degrading gracefully.
func (c *StateCollector) Collect(ctx context.Context) KubernetesState {
	if c == nil || c.service == nil {
		return KubernetesState{Status: KubernetesStatusUnavailable}
	}
	return c.service.GetState(ctx)
}
