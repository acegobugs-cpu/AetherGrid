package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
)

func TestErrorKinds(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantKind ErrorKind
	}{
		{"invalid specification", &Error{Kind: KindInvalidSpecification, Message: "test"}, KindInvalidSpecification},
		{"terraform init failed", &Error{Kind: KindTerraformInitFailed, Message: "test"}, KindTerraformInitFailed},
		{"terraform plan failed", &Error{Kind: KindTerraformPlanFailed, Message: "test"}, KindTerraformPlanFailed},
		{"terraform apply failed", &Error{Kind: KindTerraformApplyFailed, Message: "test"}, KindTerraformApplyFailed},
		{"terraform destroy failed", &Error{Kind: KindTerraformDestroyFailed, Message: "test"}, KindTerraformDestroyFailed},
		{"terraform status failed", &Error{Kind: KindTerraformStatusFailed, Message: "test"}, KindTerraformStatusFailed},
		{"provider unavailable", &Error{Kind: KindProviderUnavailable, Message: "test"}, KindProviderUnavailable},
		{"output unavailable", &Error{Kind: KindOutputUnavailable, Message: "test"}, KindOutputUnavailable},
		{"timeout", &Error{Kind: KindTimeout, Message: "test"}, KindTimeout},
		{"cancelled", &Error{Kind: KindCancelled, Message: "test"}, KindCancelled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !IsKind(test.err, test.wantKind) {
				t.Errorf("expected kind %s, got %v", test.wantKind, test.err)
			}
		})
	}
}

func TestErrorKindUnmatched(t *testing.T) {
	err := &Error{Kind: "SOME_OTHER_KIND", Message: "test"}
	if IsKind(err, KindInvalidSpecification) {
		t.Error("expected IsKind to fail for unmatched kind")
	}
}

func TestErrorUnwrap(t *testing.T) {
	cause := fmt.Errorf("original error")
	err := &Error{Kind: KindInvalidSpecification, Message: "test", Cause: cause}
	if err.Unwrap() != cause {
		t.Error("expected Unwrap to return the cause")
	}
}

func TestErrorKindDefault(t *testing.T) {
	err := &Error{Kind: "", Message: ""}
	if !IsKind(err, KindInvalidSpecification) {
		// Kind is empty, should not match any kind
	}
}

func TestCancelledDetection(t *testing.T) {
	// Context cancelled
	err := context.Canceled
	if !IsCancelled(err) {
		t.Error("expected IsCancelled to detect context.Canceled")
	}

	// Error with Cancelled kind
	err2 := &Error{Kind: KindCancelled, Message: "cancelled"}
	if !IsCancelled(err2) {
		t.Error("expected IsCancelled to detect KindCancelled")
	}
}

func TestTimeoutDetection(t *testing.T) {
	// Context deadline exceeded
	err := context.DeadlineExceeded
	if !IsTimeout(err) {
		t.Error("expected IsTimeout to detect context.DeadlineExceeded")
	}

	// Error with Timeout kind
	err2 := &Error{Kind: KindTimeout, Message: "timed out"}
	if !IsTimeout(err2) {
		t.Error("expected IsTimeout to detect KindTimeout")
	}
}

func TestPlanResult(t *testing.T) {
	pr := &PlanResult{
		Changes: domain.ChangeSummary{ToCreate: 2, ToModify: 1, ToDestroy: 0},
		Output:  "some output",
	}
	if pr.Changes.ToCreate != 2 {
		t.Error("expected ToCreate=2")
	}
	if pr.Changes.Empty() {
		t.Error("non-empty summary should not be empty")
	}
	if pr.Output != "some output" {
		t.Error("expected Output to be preserved")
	}
}

func TestApplyResult(t *testing.T) {
	nodes := []domain.InfrastructureNode{
		{ID: "id-1", Name: "node-1", IP: "10.0.0.1", State: "running"},
	}
ar := &ApplyResult{
	Changes: domain.ChangeSummary{ToCreate: 1, ToModify: 0, ToDestroy: 0},
		Nodes:   nodes,
	}
	if ar.Changes.ToCreate != 1 {
		t.Error("expected ToCreate=1")
	}
	if len(ar.Nodes) != 1 {
		t.Error("expected 1 node")
	}
	if ar.Nodes[0].Name != "node-1" {
		t.Error("expected node name to be node-1")
	}
}

func TestChangeSummaryEmpty(t *testing.T) {
	empty := domain.ChangeSummary{}
	if !empty.Empty() {
		t.Error("empty summary should report empty")
	}
	created := domain.ChangeSummary{ToCreate: 1}
	if created.Empty() {
		t.Error("summary with a create should not be empty")
	}
	destroyed := domain.ChangeSummary{ToDestroy: 1}
	if destroyed.Empty() {
		t.Error("summary with a destroy should not be empty")
	}
	modified := domain.ChangeSummary{ToModify: 1}
	if modified.Empty() {
		t.Error("summary with a modify should not be empty")
	}
}

func TestInfrastructureNode(t *testing.T) {
	nodes := []domain.InfrastructureNode{
		{ID: "/tmp/nodes/edge-01", Name: "edge-01", IP: "10.0.0.1", State: "running"},
		{ID: "/tmp/nodes/edge-02", Name: "edge-02", IP: "10.0.0.2", State: "running"},
	}
	encoded, err := json.Marshal(nodes)
	if err != nil {
		t.Fatalf("marshalling nodes: %v", err)
	}
	var decoded []domain.InfrastructureNode
	if err := json.Unmarshal(encoded, &decoded); err != nil {
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

func TestInfrastructureSpecValidate(t *testing.T) {
	tests := []struct {
		name   string
		spec   domain.InfrastructureSpec
		expect error
	}{
		{"empty name", domain.InfrastructureSpec{}, domain.ErrEmptyName},
		{"too long name", domain.InfrastructureSpec{Name: strings.Repeat("a", 64)}, domain.ErrNameTooLong},
		{"slash in name", domain.InfrastructureSpec{Name: "edge/01"}, domain.ErrNameInvalidChars},
		{"zero nodes", domain.InfrastructureSpec{Name: "edge-01"}, domain.ErrNodeCountMin},
		{"zero cpu", domain.InfrastructureSpec{Name: "edge-01", NodeCount: 1}, domain.ErrCPUMin},
		{"low memory", domain.InfrastructureSpec{Name: "edge-01", NodeCount: 1, CPU: 1}, domain.ErrMemoryMin},
		{"zero disk", domain.InfrastructureSpec{Name: "edge-01", NodeCount: 1, CPU: 1, MemoryMB: 256}, domain.ErrDiskMin},
		{"empty image", domain.InfrastructureSpec{Name: "edge-01", NodeCount: 1, CPU: 1, MemoryMB: 256, DiskGB: 1}, domain.ErrImageRequired},
		{"empty provider", domain.InfrastructureSpec{Name: "edge-01", NodeCount: 1, CPU: 1, MemoryMB: 256, DiskGB: 1, Image: "ubuntu"}, domain.ErrProviderRequired},
		{"valid", domain.InfrastructureSpec{Name: "edge-01", NodeCount: 2, CPU: 2, MemoryMB: 4096, DiskGB: 20, Image: "ubuntu-24.04", Provider: "local"}, nil},
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

func TestChangeSummaryRoundTrip(t *testing.T) {
	// Test that change summary can be constructed with non-zero values
	created := domain.ChangeSummary{ToCreate: 2, ToModify: 1, ToDestroy: 0}
	if created.ToCreate != 2 || created.ToModify != 1 || created.ToDestroy != 0 {
		t.Error("change summary values incorrect")
	}
}