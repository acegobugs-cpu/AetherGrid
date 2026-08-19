package domain

import "testing"

func TestNodeStatusValid(t *testing.T) {
	valid := []NodeStatus{
		StatusProvisioning,
		StatusProvisioned,
		StatusConnecting,
		StatusRegistered,
		StatusConfiguring,
		StatusReady,
		StatusUnhealthy,
		StatusOffline,
		StatusRecovering,
	}
	for _, status := range valid {
		if !status.Valid() {
			t.Errorf("expected %q to be valid", status)
		}
	}
}

func TestNodeStatusInvalid(t *testing.T) {
	invalid := []NodeStatus{"", "BROKEN", "ready", "123", "READY ", "READY\n"}
	for _, status := range invalid {
		if status.Valid() {
			t.Errorf("expected %q to be invalid", status)
		}
	}
}
