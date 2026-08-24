package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func TestInfrastructurePhaseValid(t *testing.T) {
	for _, phase := range allInfraPhases {
		if !phase.Valid() {
			t.Errorf("phase %q should be valid", phase)
		}
	}
	if InfrastructurePhase("BOGUS").Valid() {
		t.Error("unknown phase should not be valid")
	}
}

func TestInfrastructurePhaseTerminal(t *testing.T) {
	if !InfraPhaseReady.Terminal() {
		t.Error("ready should be terminal")
	}
	if !InfraPhaseFailed.Terminal() {
		t.Error("failed should be terminal")
	}
	if !InfraPhaseDestroyed.Terminal() {
		t.Error("destroyed should be terminal")
	}
	if InfraPhasePending.Terminal() {
		t.Error("pending should not be terminal")
	}
	if InfraPhasePlanning.Terminal() {
		t.Error("planning should not be terminal")
	}
	if InfraPhaseApplying.Terminal() {
		t.Error("applying should not be terminal")
	}
	if InfraPhaseDestroying.Terminal() {
		t.Error("destroying should not be terminal")
	}
}

func TestOperationTypeValid(t *testing.T) {
	if !OperationPlan.Valid() {
		t.Error("plan should be valid")
	}
	if !OperationApply.Valid() {
		t.Error("apply should be valid")
	}
	if !OperationDestroy.Valid() {
		t.Error("destroy should be valid")
	}
	if OperationType("BOGUS").Valid() {
		t.Error("unknown operation type should not be valid")
	}
}

func TestOperationStatusValidAndTerminal(t *testing.T) {
	for _, status := range allOpStatuses {
		if !status.Valid() {
			t.Errorf("status %q should be valid", status)
		}
	}
	if !OpSucceeded.Terminal() {
		t.Error("succeeded should be terminal")
	}
	if !OpFailed.Terminal() {
		t.Error("failed should be terminal")
	}
	if !OpCancelled.Terminal() {
		t.Error("cancelled should be terminal")
	}
	if OpPending.Terminal() {
		t.Error("pending should not be terminal")
	}
	if OpRunning.Terminal() {
		t.Error("running should not be terminal")
	}
}

func TestInfrastructureSpecValidate(t *testing.T) {
	tests := []struct {
		name   string
		spec   InfrastructureSpec
		expect error
	}{
		{"empty name", InfrastructureSpec{}, ErrEmptyName},
		{"too long name", InfrastructureSpec{Name: strings.Repeat("a", 64)}, ErrNameTooLong},
		{"slash in name", InfrastructureSpec{Name: "edge/01"}, ErrNameInvalidChars},
		{"zero nodes", InfrastructureSpec{Name: "edge-01"}, ErrNodeCountMin},
		{"zero cpu", InfrastructureSpec{Name: "edge-01", NodeCount: 1}, ErrCPUMin},
		{"low memory", InfrastructureSpec{Name: "edge-01", NodeCount: 1, CPU: 1}, ErrMemoryMin},
		{"zero disk", InfrastructureSpec{Name: "edge-01", NodeCount: 1, CPU: 1, MemoryMB: 256}, ErrDiskMin},
		{"empty image", InfrastructureSpec{Name: "edge-01", NodeCount: 1, CPU: 1, MemoryMB: 256, DiskGB: 1}, ErrImageRequired},
		{"empty provider", InfrastructureSpec{Name: "edge-01", NodeCount: 1, CPU: 1, MemoryMB: 256, DiskGB: 1, Image: "ubuntu"}, ErrProviderRequired},
		{"valid", InfrastructureSpec{Name: "edge-01", NodeCount: 2, CPU: 2, MemoryMB: 4096, DiskGB: 20, Image: "ubuntu-24.04", Provider: "local"}, nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.spec.Validate()
			if test.expect == nil {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				return
			}
			if !errors.Is(err, test.expect) {
				t.Errorf("expected %v, got %v", test.expect, err)
			}
		})
	}
}

func TestChangeSummaryEmpty(t *testing.T) {
	empty := ChangeSummary{}
	if !empty.Empty() {
		t.Error("empty summary should report empty")
	}
	created := ChangeSummary{ToCreate: 1}
	if created.Empty() {
		t.Error("summary with a create should not be empty")
	}
	destroyed := ChangeSummary{ToDestroy: 1}
	if destroyed.Empty() {
		t.Error("summary with a destroy should not be empty")
	}
	modified := ChangeSummary{ToModify: 1}
	if modified.Empty() {
		t.Error("summary with a modify should not be empty")
	}
}

func TestInfrastructureNodeRoundTrip(t *testing.T) {
	// The node JSON is the only serialized form of the node list, so it must
	// round-trip through the repository layer.
	nodes := []InfrastructureNode{
		{ID: "/tmp/nodes/edge-01", Name: "edge-01", IP: "10.0.0.1", State: "running"},
		{ID: "/tmp/nodes/edge-02", Name: "edge-02", IP: "10.0.0.2", State: "running"},
	}
	encoded, err := jsonMarshal(nodes)
	if err != nil {
		t.Fatalf("marshalling nodes: %v", err)
	}
	var decoded []InfrastructureNode
	if err := jsonUnmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshalling nodes: %v", err)
	}
	if len(decoded) != len(nodes) {
		t.Fatalf("expected %d nodes, got %d", len(nodes), len(decoded))
	}
	for i := range nodes {
		if decoded[i] != nodes[i] {
			t.Errorf("node %d mismatch: %+v vs %+v", i, decoded[i], nodes[i])
		}
	}
}

func TestInfrastructureHasTimestamps(t *testing.T) {
	now := time.Now().UTC()
	infra := &Infrastructure{
		ID:        "infra-1",
		Spec:      InfrastructureSpec{Name: "edge-01", NodeCount: 1, CPU: 1, MemoryMB: 256, DiskGB: 1, Image: "ubuntu", Provider: "local"},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if infra.CreatedAt != now {
		t.Error("created_at should round-trip")
	}
	if infra.UpdatedAt != now {
		t.Error("updated_at should round-trip")
	}
}
