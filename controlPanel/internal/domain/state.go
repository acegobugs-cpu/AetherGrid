package domain

import "time"

// Field names used to describe state differences.
const (
	// FieldStatus is the node lifecycle status field.
	FieldStatus = "status"
	// FieldKubernetesEnabled is the kubernetes_enabled configuration field.
	FieldKubernetesEnabled = "kubernetes_enabled"
	// FieldWireGuardEnabled is the wireguard_enabled configuration field.
	FieldWireGuardEnabled = "wireguard_enabled"
)

// DesiredState is the operator-declared target configuration for a node. It is
// structured so later phases can extend it to Kubernetes, networking and other
// infrastructure configuration.
type DesiredState struct {
	Status            NodeStatus `json:"status"`
	KubernetesEnabled bool       `json:"kubernetes_enabled"`
	WireGuardEnabled  bool       `json:"wireguard_enabled"`
}

// ActualState is what the system currently observes about a node. It is never
// mutated to satisfy desired state; it only reflects observation.
type ActualState struct {
	Status            NodeStatus `json:"status"`
	KubernetesEnabled bool       `json:"kubernetes_enabled"`
	WireGuardEnabled  bool       `json:"wireguard_enabled"`
	LastHeartbeat     *time.Time `json:"last_heartbeat"`
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
	if desired.KubernetesEnabled != actual.KubernetesEnabled {
		differences = append(differences, Difference{
			Field:   FieldKubernetesEnabled,
			Desired: desired.KubernetesEnabled,
			Actual:  actual.KubernetesEnabled,
		})
	}
	if desired.WireGuardEnabled != actual.WireGuardEnabled {
		differences = append(differences, Difference{
			Field:   FieldWireGuardEnabled,
			Desired: desired.WireGuardEnabled,
			Actual:  actual.WireGuardEnabled,
		})
	}
	return differences
}
