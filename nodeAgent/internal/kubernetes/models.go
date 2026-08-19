// Package kubernetes contains the AETHER-GRID Kubernetes integration layer.
// It defines a client abstraction over client-go, a service layer with
// application logic, translated domain errors and the observed state model.
//
// The control plane never talks to Kubernetes directly; the edge agent is the
// integration boundary. All client-go usage is confined to this package.
package kubernetes

// KubernetesStatus is the derived health of a Kubernetes cluster observed by
// the agent.
type KubernetesStatus string

// Kubernetes health statuses.
const (
	// KubernetesStatusDisabled reports that Kubernetes integration is turned
	// off for this agent.
	KubernetesStatusDisabled KubernetesStatus = "DISABLED"
	// KubernetesStatusUnavailable reports that the API server could not be
	// reached or the client is not usable.
	KubernetesStatusUnavailable KubernetesStatus = "UNAVAILABLE"
	// KubernetesStatusDegraded reports that the API server is reachable but
	// some observed nodes are not ready.
	KubernetesStatusDegraded KubernetesStatus = "DEGRADED"
	// KubernetesStatusReady reports a reachable, healthy cluster.
	KubernetesStatusReady KubernetesStatus = "READY"
)

// ClusterInfo is a summary of the cluster observed by the agent.
type ClusterInfo struct {
	// Version is the reported Kubernetes server version, for example
	// "v1.31.0".
	Version string
	// NodeCount is the number of Kubernetes nodes.
	NodeCount int
	// ReadyNodes is the number of nodes with a Ready condition.
	ReadyNodes int
	// NotReadyNodes is the number of nodes without a Ready condition.
	NotReadyNodes int
}

// KubernetesNode is the observed state of one Kubernetes node.
type KubernetesNode struct {
	// Name is the Kubernetes node name.
	Name string `json:"name"`
	// Ready reports whether the node's Ready condition is true.
	Ready bool `json:"ready"`
	// KubernetesVersion is the node's kubelet version.
	KubernetesVersion string `json:"kubernetes_version"`
	// OS is the node's operating system.
	OS string `json:"os"`
	// Architecture is the node's CPU architecture.
	Architecture string `json:"architecture"`
	// InternalIP is the node's internal IP address, when available.
	InternalIP string `json:"internal_ip"`
	// Roles are the node's role labels, when present.
	Roles []string `json:"roles,omitempty"`
}

// KubernetesPod is the observed state of one pod.
type KubernetesPod struct {
	// Namespace is the pod's namespace.
	Namespace string `json:"namespace"`
	// Name is the pod's name.
	Name string `json:"name"`
	// Phase is the pod phase (Pending, Running, Succeeded, Failed, Unknown).
	Phase string `json:"phase"`
	// NodeName is the node the pod is scheduled on, when assigned.
	NodeName string `json:"node_name,omitempty"`
	// RestartCount is the total number of container restarts.
	RestartCount int `json:"restart_count"`
}

// WorkloadSummary is a lightweight aggregate of pod state. The agent never
// collects secrets and never mirrors every workload into the control plane.
type WorkloadSummary struct {
	// TotalPods is the number of pods observed.
	TotalPods int `json:"total_pods"`
	// RunningPods is the number of pods in the Running phase.
	RunningPods int `json:"running_pods"`
	// FailedPods is the number of pods in the Failed phase.
	FailedPods int `json:"failed_pods"`
}

// KubernetesState is the observed state of the Kubernetes cluster reported by
// the agent. It is kept separate from the generic node state so a node can be
// Agent=READY while Kubernetes=UNAVAILABLE.
type KubernetesState struct {
	// Available reports whether the Kubernetes API server is reachable.
	Available bool `json:"available"`
	// Status is the derived health (DISABLED, UNAVAILABLE, DEGRADED, READY).
	Status KubernetesStatus `json:"status"`
	// Version is the Kubernetes server version.
	Version string `json:"version"`
	// NodeCount is the number of Kubernetes nodes.
	NodeCount int `json:"node_count"`
	// ReadyNodes is the number of ready Kubernetes nodes.
	ReadyNodes int `json:"ready_nodes"`
	// NotReadyNodes is the number of not-ready Kubernetes nodes.
	NotReadyNodes int `json:"not_ready_nodes"`
	// Workload is the basic pod summary.
	Workload WorkloadSummary `json:"workload"`
	// Error carries a translated error when the cluster is unavailable. It is
	// never sent to the control plane and never contains credentials.
	Error string `json:"-"`
}
