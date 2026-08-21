package domain

import "time"

// KubernetesStatus is the health status reported by a node's Kubernetes
// integration layer. It is deliberately separate from the node lifecycle
// status: a node can be READY while its Kubernetes integration is UNAVAILABLE.
type KubernetesStatus string

// Kubernetes integration statuses observed by the agent.
const (
	KubernetesDisabled    KubernetesStatus = "DISABLED"
	KubernetesUnavailable KubernetesStatus = "UNAVAILABLE"
	KubernetesDegraded    KubernetesStatus = "DEGRADED"
	KubernetesReady       KubernetesStatus = "READY"
)

// Field names used to describe state differences.
const (
	// FieldStatus is the node lifecycle status field.
	FieldStatus = "status"
	// FieldKubernetesAvailable is the Kubernetes availability field.
	FieldKubernetesAvailable = "kubernetes.available"
	// FieldKubernetesReadyNodes is the Kubernetes ready-node count field.
	FieldKubernetesReadyNodes = "kubernetes.ready_nodes"
	// FieldWireGuardEnabled is the wireguard_enabled configuration field.
	FieldWireGuardEnabled = "wireguard_enabled"
)

// KubernetesDesiredState is the operator-declared Kubernetes expectation for a
// node. The reconciliation engine uses it to detect drift between the declared
// intent and the agent's observed Kubernetes state.
type KubernetesDesiredState struct {
	// Enabled declares that the node's Kubernetes integration should be
	// available. When false, no Kubernetes expectations are enforced.
	Enabled bool `json:"enabled"`
	// MinimumReadyNodes is the minimum number of Ready nodes the cluster must
	// report for the node's Kubernetes integration to be in sync.
	MinimumReadyNodes int `json:"minimum_ready_nodes"`
}

// WorkloadSummary is the observed pod workload of a Kubernetes cluster.
type WorkloadSummary struct {
	TotalPods   int `json:"total_pods"`
	RunningPods int `json:"running_pods"`
	FailedPods  int `json:"failed_pods"`
}

// KubernetesActualState is what the agent observes about the Kubernetes
// cluster. It reflects observation only and is never mutated to satisfy
// desired state.
type KubernetesActualState struct {
	Available     bool             `json:"available"`
	Status        KubernetesStatus `json:"status"`
	Version       string           `json:"version"`
	NodeCount     int              `json:"node_count"`
	ReadyNodes    int              `json:"ready_nodes"`
	NotReadyNodes int              `json:"not_ready_nodes"`
	Workload      WorkloadSummary  `json:"workload"`
	// ReportedAt is when the agent last reported this observation.
	ReportedAt time.Time `json:"reported_at"`
}

// DesiredState is the operator-declared target configuration for a node. It is
// structured so later phases can extend it to networking and other
// infrastructure configuration.
type DesiredState struct {
	Status           NodeStatus             `json:"status"`
	Kubernetes       KubernetesDesiredState `json:"kubernetes"`
	WireGuardEnabled bool                   `json:"wireguard_enabled"`
}

// ActualState is what the system currently observes about a node. It is never
// mutated to satisfy desired state; it only reflects observation.
type ActualState struct {
	Status           NodeStatus             `json:"status"`
	Kubernetes       *KubernetesActualState `json:"kubernetes,omitempty"`
	WireGuardEnabled bool                   `json:"wireguard_enabled"`
	LastHeartbeat    *time.Time             `json:"last_heartbeat"`
}

// Difference explains why two states differ. The controller uses structured
// differences to decide what action to take.
type Difference struct {
	Field   string `json:"field"`
	Desired any    `json:"desired"`
	Actual  any    `json:"actual"`
}

// CompareStates compares a desired state against an observed actual state and
// returns one Difference per differing field. A nil/empty result means the
// states are in sync.
func CompareStates(desired DesiredState, actual ActualState) []Difference {
	var differences []Difference
	if desired.Status != actual.Status {
		differences = append(differences, Difference{
			Field:   FieldStatus,
			Desired: desired.Status,
			Actual:  actual.Status,
		})
	}
	if desired.WireGuardEnabled != actual.WireGuardEnabled {
		differences = append(differences, Difference{
			Field:   FieldWireGuardEnabled,
			Desired: desired.WireGuardEnabled,
			Actual:  actual.WireGuardEnabled,
		})
	}
	differences = append(differences, compareKubernetes(desired, actual)...)
	return differences
}

// compareKubernetes detects drift between the declared Kubernetes expectation
// and the observed Kubernetes state. Kubernetes is only enforced when desired;
// when disabled no Kubernetes expectations are checked. A missing observation
// is treated as unavailable so a node whose desired Kubernetes integration
// has not reported yet is surfaced as drifted.
func compareKubernetes(desired DesiredState, actual ActualState) []Difference {
	if !desired.Kubernetes.Enabled {
		return nil
	}

	var available bool
	var readyNodes int
	if actual.Kubernetes != nil {
		available = actual.Kubernetes.Available
		readyNodes = actual.Kubernetes.ReadyNodes
	}

	var differences []Difference
	if !available {
		return append(differences, Difference{
			Field:   FieldKubernetesAvailable,
			Desired: true,
			Actual:  false,
		})
	}
	if desired.Kubernetes.MinimumReadyNodes > 0 &&
		readyNodes < desired.Kubernetes.MinimumReadyNodes {
		differences = append(differences, Difference{
			Field:   FieldKubernetesReadyNodes,
			Desired: desired.Kubernetes.MinimumReadyNodes,
			Actual:  readyNodes,
		})
	}
	return differences
}
