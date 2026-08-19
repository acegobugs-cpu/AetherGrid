package domain

import "testing"

func TestCompareStatesInSync(t *testing.T) {
	desired := DesiredState{
		Status: StatusReady,
		Kubernetes: KubernetesDesiredState{
			Enabled:           true,
			MinimumReadyNodes: 1,
		},
		WireGuardEnabled: false,
	}
	actual := ActualState{
		Status: StatusReady,
		Kubernetes: &KubernetesActualState{
			Available:  true,
			Status:     KubernetesReady,
			ReadyNodes: 1,
		},
		WireGuardEnabled: false,
	}

	differences := CompareStates(desired, actual)
	if len(differences) != 0 {
		t.Errorf("expected no differences, got %v", differences)
	}
}

func TestCompareStatesStatusMismatch(t *testing.T) {
	desired := DesiredState{Status: StatusReady}
	actual := ActualState{Status: StatusOffline}

	differences := CompareStates(desired, actual)
	if len(differences) != 1 {
		t.Fatalf("expected 1 difference, got %v", differences)
	}
	if differences[0].Field != FieldStatus {
		t.Errorf("expected status field, got %s", differences[0].Field)
	}
	if differences[0].Desired != StatusReady || differences[0].Actual != StatusOffline {
		t.Errorf("unexpected difference values: %+v", differences[0])
	}
}

func TestCompareStatesAllFields(t *testing.T) {
	desired := DesiredState{
		Status: StatusReady,
		Kubernetes: KubernetesDesiredState{
			Enabled:           true,
			MinimumReadyNodes: 2,
		},
		WireGuardEnabled: true,
	}
	actual := ActualState{
		Status: StatusProvisioning,
		Kubernetes: &KubernetesActualState{
			Available:  false,
			Status:     KubernetesUnavailable,
			ReadyNodes: 0,
		},
		WireGuardEnabled: false,
	}

	differences := CompareStates(desired, actual)
	if len(differences) != 3 {
		t.Fatalf("expected 3 differences, got %v", differences)
	}

	fields := map[string]bool{}
	for _, difference := range differences {
		fields[difference.Field] = true
	}
	for _, field := range []string{FieldStatus, FieldKubernetesAvailable, FieldWireGuardEnabled} {
		if !fields[field] {
			t.Errorf("expected difference for field %s, got %v", field, fields)
		}
	}
}

// TestCompareStatesKubernetesUnavailable verifies the spec section 22 scenario:
// desired enabled with minimum_ready_nodes while the actual Kubernetes state
// is unavailable must be surfaced as a kubernetes.available drift.
func TestCompareStatesKubernetesUnavailable(t *testing.T) {
	desired := DesiredState{
		Status: StatusReady,
		Kubernetes: KubernetesDesiredState{
			Enabled:           true,
			MinimumReadyNodes: 1,
		},
	}
	actual := ActualState{
		Status:     StatusReady,
		Kubernetes: &KubernetesActualState{Available: false, Status: KubernetesUnavailable},
	}

	differences := CompareStates(desired, actual)
	if len(differences) != 1 {
		t.Fatalf("expected 1 difference, got %v", differences)
	}
	if differences[0].Field != FieldKubernetesAvailable {
		t.Fatalf("expected kubernetes.available drift, got %s", differences[0].Field)
	}
	if differences[0].Desired != true || differences[0].Actual != false {
		t.Errorf("unexpected drift values: %+v", differences[0])
	}
}

// TestCompareStatesKubernetesNotEnoughReadyNodes verifies ready-node drift:
// the cluster is available but reports fewer Ready nodes than required.
func TestCompareStatesKubernetesNotEnoughReadyNodes(t *testing.T) {
	desired := DesiredState{
		Status: StatusReady,
		Kubernetes: KubernetesDesiredState{
			Enabled:           true,
			MinimumReadyNodes: 3,
		},
	}
	actual := ActualState{
		Status: StatusReady,
		Kubernetes: &KubernetesActualState{
			Available:  true,
			Status:     KubernetesDegraded,
			ReadyNodes: 1,
			NodeCount:  3,
		},
	}

	differences := CompareStates(desired, actual)
	if len(differences) != 1 {
		t.Fatalf("expected 1 difference, got %v", differences)
	}
	if differences[0].Field != FieldKubernetesReadyNodes {
		t.Fatalf("expected kubernetes.ready_nodes drift, got %s", differences[0].Field)
	}
	if differences[0].Desired != 3 || differences[0].Actual != 1 {
		t.Errorf("unexpected drift values: %+v", differences[0])
	}
}

// TestCompareStatesKubernetesMissingObservation treats an absent Kubernetes
// report as unavailable so an enabled expectation is surfaced as drifted.
func TestCompareStatesKubernetesMissingObservation(t *testing.T) {
	desired := DesiredState{
		Status: StatusReady,
		Kubernetes: KubernetesDesiredState{
			Enabled:           true,
			MinimumReadyNodes: 1,
		},
	}
	actual := ActualState{Status: StatusReady}

	differences := CompareStates(desired, actual)
	if len(differences) != 1 || differences[0].Field != FieldKubernetesAvailable {
		t.Fatalf("expected kubernetes.available drift for missing observation, got %v", differences)
	}
}

// TestCompareStatesKubernetesDisabled ignores Kubernetes when not desired.
func TestCompareStatesKubernetesDisabled(t *testing.T) {
	desired := DesiredState{Status: StatusReady}
	actual := ActualState{
		Status:     StatusReady,
		Kubernetes: &KubernetesActualState{Available: false, Status: KubernetesUnavailable},
	}

	differences := CompareStates(desired, actual)
	if len(differences) != 0 {
		t.Fatalf("expected no differences when Kubernetes is not desired, got %v", differences)
	}
}

func TestNodeStructuredStateHelpers(t *testing.T) {
	node := &Node{
		ID:                          "edge-01",
		Status:                      StatusReady,
		DesiredStatus:               StatusReady,
		KubernetesEnabled:           true,
		KubernetesMinimumReadyNodes: 2,
		WireGuardEnabled:            true,
	}

	desired := node.DesiredState()
	if desired.Status != StatusReady || !desired.Kubernetes.Enabled || desired.Kubernetes.MinimumReadyNodes != 2 || !desired.WireGuardEnabled {
		t.Errorf("unexpected desired state: %+v", desired)
	}

	actual := node.ActualState()
	if actual.Status != StatusReady || actual.Kubernetes != nil || !actual.WireGuardEnabled {
		t.Errorf("unexpected actual state: %+v", actual)
	}
	if actual.LastHeartbeat != nil {
		t.Error("expected nil heartbeat for a fresh node")
	}
}

func TestReconciliationStatusValues(t *testing.T) {
	statuses := []ReconciliationStatus{
		ReconciliationInSync,
		ReconciliationDriftDetected,
		ReconciliationReconciling,
		ReconciliationReconciled,
		ReconciliationFailed,
	}
	for _, status := range statuses {
		if status == "" {
			t.Error("reconciliation status must not be empty")
		}
	}
}
