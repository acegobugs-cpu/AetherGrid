package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AetherClusterPhase is the high-level lifecycle phase of an AetherCluster.
//
// Phases follow a deterministic lifecycle:
//
//	Pending ──► Progressing ──► Ready
//	              │  │
//	              │  └────► Degraded
//	              └────► Failed
type AetherClusterPhase string

const (
	// PhasePending indicates the resource was accepted but reconciliation has
	// not begun.
	PhasePending AetherClusterPhase = "Pending"
	// PhaseProgressing indicates the operator is working toward the desired
	// state (for example creating or updating the Deployment).
	PhaseProgressing AetherClusterPhase = "Progressing"
	// PhaseReady indicates the Deployment is running the desired replicas.
	PhaseReady AetherClusterPhase = "Ready"
	// PhaseDegraded indicates the Deployment is running but not healthy.
	PhaseDegraded AetherClusterPhase = "Degraded"
	// PhaseFailed indicates the desired state cannot be realized.
	PhaseFailed AetherClusterPhase = "Failed"
)

// Condition types on the AetherCluster status.
const (
	// ConditionReady reports whether the desired Deployment is running the
	// desired replicas.
	ConditionReady = "Ready"
	// ConditionProgressing reports whether the operator is actively converging
	// the Deployment toward the desired state.
	ConditionProgressing = "Progressing"
	// ConditionDegraded reports whether the Deployment is in a persistent
	// unhealthy state that the operator cannot immediately resolve.
	ConditionDegraded = "Degraded"
)

// Condition reasons on the AetherCluster status.
const (
	ReasonCreatingDeployment    = "CreatingDeployment"
	ReasonUpdatingDeployment    = "UpdatingDeployment"
	ReasonDeploymentReady       = "DeploymentReady"
	ReasonDeploymentProgressing = "DeploymentProgressing"
	ReasonDeploymentDegraded    = "DeploymentDegraded"
	ReasonValidationFailed      = "ValidationFailed"
	ReasonReconcileError        = "ReconcileError"
)

// AetherClusterSpec defines the desired Kubernetes state managed by
// AETHER-GRID. It represents intent, not a dump of the cluster.
type AetherClusterSpec struct {
	// Replicas is the desired replica count for the managed Deployment.
	// Defaults to 1 when unset.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	Replicas *int32 `json:"replicas,omitempty"`

	// Image is the container image for the managed workload.
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// Port is the container port exposed by the workload, when appropriate.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port,omitempty"`
}

// AetherClusterStatus reflects the observed state of the managed Deployment.
// It is written only when it actually changes.
type AetherClusterStatus struct {
	// Phase is the high-level lifecycle phase.
	// +optional
	Phase AetherClusterPhase `json:"phase,omitempty"`

	// ReadyReplicas is the number of ready replicas observed on the Deployment.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// DesiredReplicas is the number of replicas the Deployment targets.
	// +optional
	DesiredReplicas int32 `json:"desiredReplicas,omitempty"`

	// ObservedGeneration records the generation of the spec that was last
	// processed by the operator.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions follow Kubernetes conventions.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=agc
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Desired",type=string,JSONPath=`.status.desiredReplicas`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AetherCluster is the Schema for the aetherclusters API. It declares the
// desired Kubernetes state AETHER-GRID maintains.
type AetherCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AetherClusterSpec   `json:"spec,omitempty"`
	Status AetherClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AetherClusterList contains a list of AetherCluster.
type AetherClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AetherCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AetherCluster{}, &AetherClusterList{})
}
