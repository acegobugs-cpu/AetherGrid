package kubernetes

import (
	"context"
	"testing"
)

func TestCollectorDisabled(t *testing.T) {
	service := NewService(ServiceConfig{Enabled: false}, nil, quietLogger())
	collector := NewStateCollector(service)
	state := collector.Collect(context.Background())
	if state.Status != KubernetesStatusDisabled {
		t.Fatalf("expected DISABLED, got %s", state.Status)
	}
}

func TestCollectorUnavailable(t *testing.T) {
	service := testService(&mockClient{clusterInfoErr: &Error{Code: CodeUnavailable}})
	collector := NewStateCollector(service)
	state := collector.Collect(context.Background())
	if state.Status != KubernetesStatusUnavailable {
		t.Fatalf("expected UNAVAILABLE, got %s", state.Status)
	}
}

func TestCollectorReady(t *testing.T) {
	service := testService(&mockClient{
		clusterInfo: ClusterInfo{Version: "v1.31.0", NodeCount: 1, ReadyNodes: 1},
	})
	collector := NewStateCollector(service)
	state := collector.Collect(context.Background())
	if state.Status != KubernetesStatusReady {
		t.Fatalf("expected READY, got %s", state.Status)
	}
}

func TestCollectorNilService(t *testing.T) {
	var collector *StateCollector
	state := collector.Collect(context.Background())
	if state.Status != KubernetesStatusUnavailable {
		t.Fatalf("expected UNAVAILABLE for nil service, got %s", state.Status)
	}
}
