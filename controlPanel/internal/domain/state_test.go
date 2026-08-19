package domain

import "testing"

func TestCompareStatesInSync(t *testing.T) {
	desired := DesiredState{Status: StatusReady, KubernetesEnabled: true, WireGuardEnabled: false}
	actual := ActualState{Status: StatusReady, KubernetesEnabled: true, WireGuardEnabled: false}

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
	desired := DesiredState{Status: StatusReady, KubernetesEnabled: true, WireGuardEnabled: true}
	actual := ActualState{Status: StatusProvisioning, KubernetesEnabled: false, WireGuardEnabled: false}

	differences := CompareStates(desired, actual)
	if len(differences) != 3 {
		t.Fatalf("expected 3 differences, got %v", differences)
	}

	fields := map[string]bool{}
	for _, difference := range differences {
		fields[difference.Field] = true
	}
	for _, field := range []string{FieldStatus, FieldKubernetesEnabled, FieldWireGuardEnabled} {
		if !fields[field] {
			t.Errorf("expected difference for field %s, got %v", field, fields)
		}
	}
}

func TestNodeStructuredStateHelpers(t *testing.T) {
	node := &Node{
		ID:                "edge-01",
		Status:            StatusReady,
		DesiredStatus:     StatusReady,
		KubernetesEnabled: true,
		WireGuardEnabled:  true,
	}

	desired := node.DesiredState()
	if desired.Status != StatusReady || !desired.KubernetesEnabled || !desired.WireGuardEnabled {
		t.Errorf("unexpected desired state: %+v", desired)
	}

	actual := node.ActualState()
	if actual.Status != StatusReady || !actual.KubernetesEnabled || !actual.WireGuardEnabled {
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
