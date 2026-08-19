package controller

import "time"

// Managed labels applied to the Deployment and its pods. Only these labels are
// owned by the operator: user-added labels are never removed.
const (
	// LabelManaged marks resources created and owned by the operator.
	LabelManaged = "aether-grid.io/managed"
	// LabelManagedBy identifies the controlling process.
	LabelManagedBy = "app.kubernetes.io/managed-by"
	// LabelName identifies the workload derived from the AetherCluster name.
	LabelName = "app.kubernetes.io/name"
	// LabelPartOf groups resources belonging to AETHER-GRID.
	LabelPartOf = "app.kubernetes.io/part-of"
)

// OperatorName is the value used in the managed-by label.
const OperatorName = "aether-grid-operator"

// RequeueInterval is used while the workload is converging so the operator
// keeps observing asynchronous Deployment/Pod changes even if watch events are
// missed. Controller-runtime backoff governs retry behaviour on errors.
const RequeueInterval = 5 * time.Second
