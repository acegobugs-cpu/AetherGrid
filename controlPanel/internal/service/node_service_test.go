package service

import (
	"context"
	"errors"
	"testing"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
	"github.com/acegobugs-cpu/AetherGrid/internal/repository"
)

func TestNodeServiceCreate(t *testing.T) {
	svc := NewNodeService(newMockNodeRepository())

	node, err := svc.Create(context.Background(), CreateNodeInput{
		Name:              "edge-01",
		Location:          "addis-01",
		IPAddress:         "10.0.0.10",
		KubernetesEnabled: true,
		WireGuardEnabled:  true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if node.ID == "" {
		t.Error("expected node to have a generated ID")
	}
	if node.Status != domain.InitialStatus {
		t.Errorf("expected initial status %q, got %q", domain.InitialStatus, node.Status)
	}
	if node.DesiredStatus != domain.DesiredInitialStatus {
		t.Errorf("expected desired status %q, got %q", domain.DesiredInitialStatus, node.DesiredStatus)
	}
	if node.Name != "edge-01" {
		t.Errorf("expected name edge-01, got %q", node.Name)
	}
	if !node.KubernetesEnabled || !node.WireGuardEnabled {
		t.Error("expected kubernetes and wireguard to be enabled")
	}
	if node.LastHeartbeat != nil {
		t.Error("expected no heartbeat on a fresh node")
	}
}

func TestNodeServiceCreateValidation(t *testing.T) {
	svc := NewNodeService(newMockNodeRepository())

	tests := []struct {
		name  string
		input CreateNodeInput
	}{
		{"empty name", CreateNodeInput{}},
		{"blank name", CreateNodeInput{Name: "   "}},
		{"invalid ip", CreateNodeInput{Name: "edge-01", IPAddress: "not-an-ip"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := svc.Create(context.Background(), test.input)
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("expected ValidationError, got %v", err)
			}
		})
	}
}

func TestNodeServiceCreateDuplicateName(t *testing.T) {
	svc := NewNodeService(newMockNodeRepository())

	input := CreateNodeInput{Name: "edge-01", IPAddress: "10.0.0.10"}
	if _, err := svc.Create(context.Background(), input); err != nil {
		t.Fatalf("first create failed: %v", err)
	}
	if _, err := svc.Create(context.Background(), input); err == nil {
		t.Fatal("expected duplicate name to fail")
	} else if !IsConflict(err) {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestNodeServiceGetNotFound(t *testing.T) {
	svc := NewNodeService(newMockNodeRepository())

	_, err := svc.Get(context.Background(), "missing")
	if !IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestNodeServiceListAndDelete(t *testing.T) {
	svc := NewNodeService(newMockNodeRepository())

	for _, name := range []string{"edge-01", "edge-02"} {
		if _, err := svc.Create(context.Background(), CreateNodeInput{Name: name}); err != nil {
			t.Fatalf("create %s failed: %v", name, err)
		}
	}

	nodes, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}

	id := nodes[0].ID
	if err := svc.Delete(context.Background(), id); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if _, err := svc.Get(context.Background(), id); !IsNotFound(err) {
		t.Fatalf("expected node to be gone, got %v", err)
	}
}

func TestNodeServiceSetDesiredStatus(t *testing.T) {
	svc := NewNodeService(newMockNodeRepository())

	node, err := svc.Create(context.Background(), CreateNodeInput{Name: "edge-01"})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	updated, err := svc.SetDesiredStatus(context.Background(), node.ID, domain.StatusReady)
	if err != nil {
		t.Fatalf("set desired status failed: %v", err)
	}
	if updated.DesiredStatus != domain.StatusReady {
		t.Errorf("expected desired status READY, got %q", updated.DesiredStatus)
	}

	_, err = svc.SetDesiredStatus(context.Background(), node.ID, domain.NodeStatus("BOGUS"))
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected ValidationError for bogus status, got %v", err)
	}
}

func TestNodeServiceSetDesiredStatusNotFound(t *testing.T) {
	svc := NewNodeService(newMockNodeRepository())

	_, err := svc.SetDesiredStatus(context.Background(), "missing", domain.StatusReady)
	if !IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestValidateNewNode(t *testing.T) {
	if err := ValidateNewNode(CreateNodeInput{Name: "edge-01", IPAddress: "10.0.0.1"}); err != nil {
		t.Errorf("expected valid input, got %v", err)
	}
	if err := ValidateNewNode(CreateNodeInput{Name: "edge-01", IPAddress: "300.1.1.1"}); err == nil {
		t.Error("expected invalid IP to fail")
	}
}

func TestRepositoryErrorPredicates(t *testing.T) {
	if IsNotFound(repository.ErrNotFound) != true {
		t.Error("IsNotFound should report true for ErrNotFound")
	}
	if IsConflict(repository.ErrConflict) != true {
		t.Error("IsConflict should report true for ErrConflict")
	}
	if IsNotFound(errors.New("other")) {
		t.Error("IsNotFound should report false for unrelated errors")
	}
}
