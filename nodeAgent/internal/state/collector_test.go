package state

import (
	"testing"
)

func TestLocalCollectorCollect(t *testing.T) {
	collector := NewLocalCollector("test-version")

	collected, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	if collected.Hostname == "" {
		t.Error("expected a hostname")
	}
	if collected.OS == "" || collected.Architecture == "" {
		t.Error("expected os and architecture to be set")
	}
	if collected.CPUCount <= 0 {
		t.Errorf("expected a positive cpu count, got %d", collected.CPUCount)
	}
	if collected.AgentVersion != "test-version" {
		t.Errorf("expected version test-version, got %q", collected.AgentVersion)
	}
	if collected.Status != StatusReady {
		t.Errorf("expected collector default status READY, got %q", collected.Status)
	}
}

func TestParseMemTotal(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    uint64
	}{
		{"typical", "MemTotal:        8000000 kB\nMemFree:         1000000 kB\n", 8000000 * 1024},
		{"zero", "MemTotal:        0 kB\n", 0},
		{"missing field", "MemTotal:\n", 0},
		{"garbage value", "MemTotal: not-a-number kB\n", 0},
		{"empty", "", 0},
		{"no memtotal line", "MemFree: 100 kB\n", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := parseMemTotal(test.content); got != test.want {
				t.Errorf("expected %d, got %d", test.want, got)
			}
		})
	}
}

func TestParseUptime(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int64
	}{
		{"typical", "12345.67 80000.12", 12345},
		{"truncated", "12.5", 12},
		{"garbage", "garbage", 0},
		{"empty", "", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := parseUptime(test.content); got != test.want {
				t.Errorf("expected %d, got %d", test.want, got)
			}
		})
	}
}

func TestValidateStatus(t *testing.T) {
	for _, status := range []AgentStatus{StatusStarting, StatusReady, StatusDegraded, StatusStopping} {
		if err := ValidateStatus(status); err != nil {
			t.Errorf("expected %q to be valid, got %v", status, err)
		}
	}
	if err := ValidateStatus(AgentStatus("BOGUS")); err == nil {
		t.Error("expected BOGUS to be invalid")
	}
}
