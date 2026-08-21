package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"AetherGrid/controlPlane/internal/domain"
)

func TestCommandServiceCreate(t *testing.T) {
	nodes := newMockNodeRepository()
	svc := NewCommandService(newMockCommandRepository(), nodes)

	if err := nodes.Create(context.Background(), &domain.Node{ID: "node-1", Name: "edge-01"}); err != nil {
		t.Fatalf("creating node: %v", err)
	}

	command, err := svc.Create(context.Background(), CreateCommandInput{
		NodeID:     "node-1",
		Type:       "GET_STATUS",
		Parameters: json.RawMessage(`{"detail":"full"}`),
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if command.ID == "" {
		t.Error("expected a generated command ID")
	}
	if command.Status != domain.CommandPending {
		t.Errorf("expected PENDING, got %q", command.Status)
	}
	if command.NodeID != "node-1" || command.Type != "GET_STATUS" {
		t.Errorf("unexpected command: %+v", command)
	}
}

func TestCommandServiceCreateValidation(t *testing.T) {
	svc := NewCommandService(newMockCommandRepository(), newMockNodeRepository())

	_, err := svc.Create(context.Background(), CreateCommandInput{NodeID: "node-1"})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected ValidationError for empty type, got %v", err)
	}
}

func TestCommandServiceCreateUnknownNode(t *testing.T) {
	svc := NewCommandService(newMockCommandRepository(), newMockNodeRepository())

	_, err := svc.Create(context.Background(), CreateCommandInput{NodeID: "missing", Type: "GET_STATUS"})
	if !IsNotFound(err) {
		t.Fatalf("expected not found for missing node, got %v", err)
	}
}

func TestCommandServiceListByNode(t *testing.T) {
	commands := newMockCommandRepository()
	nodes := newMockNodeRepository()
	svc := NewCommandService(commands, nodes)

	if err := nodes.Create(context.Background(), &domain.Node{ID: "node-1", Name: "edge-01"}); err != nil {
		t.Fatalf("creating node: %v", err)
	}
	if err := nodes.Create(context.Background(), &domain.Node{ID: "node-2", Name: "edge-02"}); err != nil {
		t.Fatalf("creating node: %v", err)
	}

	first, _ := svc.Create(context.Background(), CreateCommandInput{NodeID: "node-1", Type: "GET_STATUS"})
	svc.Create(context.Background(), CreateCommandInput{NodeID: "node-2", Type: "GET_STATUS"})

	// A PENDING status filter returns only pending commands.
	svc.ReportResult(context.Background(), ReportResultInput{
		NodeID: "node-1", CommandID: first.ID, Status: domain.CommandSucceeded,
		Result: json.RawMessage(`{"status":"READY"}`),
	})

	got, err := svc.ListByNode(context.Background(), "node-1", domain.CommandPending)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no pending commands, got %d", len(got))
	}

	all, err := svc.ListByNode(context.Background(), "node-1", "")
	if err != nil {
		t.Fatalf("list all failed: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 command for node-1, got %d", len(all))
	}
}

func TestCommandServiceReportResult(t *testing.T) {
	commands := newMockCommandRepository()
	svc := NewCommandService(commands, newMockNodeRepository())

	seed := &domain.Command{
		ID: "cmd-1", NodeID: "node-1", Type: "GET_STATUS", Status: domain.CommandPending,
	}
	if err := commands.Create(context.Background(), seed); err != nil {
		t.Fatalf("seeding command: %v", err)
	}

	updated, err := svc.ReportResult(context.Background(), ReportResultInput{
		NodeID: "node-1", CommandID: seed.ID, Status: domain.CommandSucceeded,
		Result: json.RawMessage(`{"status":"READY"}`),
	})
	if err != nil {
		t.Fatalf("report result failed: %v", err)
	}
	if updated.Status != domain.CommandSucceeded {
		t.Errorf("expected SUCCEEDED, got %q", updated.Status)
	}
	if string(updated.Result) != `{"status":"READY"}` {
		t.Errorf("expected result preserved, got %q", updated.Result)
	}
}

func TestCommandServiceReportResultIdempotent(t *testing.T) {
	commands := newMockCommandRepository()
	svc := NewCommandService(commands, newMockNodeRepository())

	seed := &domain.Command{
		ID: "cmd-1", NodeID: "node-1", Type: "GET_STATUS", Status: domain.CommandSucceeded,
		Result: json.RawMessage(`{"status":"READY"}`),
	}
	if err := commands.Create(context.Background(), seed); err != nil {
		t.Fatalf("seeding command: %v", err)
	}

	// A second report for an already-terminal command must be ignored.
	updated, err := svc.ReportResult(context.Background(), ReportResultInput{
		NodeID: "node-1", CommandID: seed.ID, Status: domain.CommandFailed,
		Error: "later report",
	})
	if err != nil {
		t.Fatalf("report result failed: %v", err)
	}
	if updated.Status != domain.CommandSucceeded {
		t.Errorf("expected terminal status preserved, got %q", updated.Status)
	}
}

func TestCommandServiceReportResultCrossNodeRejected(t *testing.T) {
	commands := newMockCommandRepository()
	svc := NewCommandService(commands, newMockNodeRepository())

	seed := &domain.Command{
		ID: "cmd-1", NodeID: "node-1", Type: "GET_STATUS", Status: domain.CommandPending,
	}
	if err := commands.Create(context.Background(), seed); err != nil {
		t.Fatalf("seeding command: %v", err)
	}

	_, err := svc.ReportResult(context.Background(), ReportResultInput{
		NodeID: "node-2", CommandID: seed.ID, Status: domain.CommandSucceeded,
	})
	if !IsCommandNotFound(err) {
		t.Fatalf("expected not found for cross-node report, got %v", err)
	}

	// The command must remain pending.
	got, _ := commands.GetByID(context.Background(), seed.ID)
	if got.Status != domain.CommandPending {
		t.Errorf("expected command untouched, got %q", got.Status)
	}
}

func TestCommandServiceReportResultInvalidStatus(t *testing.T) {
	svc := NewCommandService(newMockCommandRepository(), newMockNodeRepository())

	_, err := svc.ReportResult(context.Background(), ReportResultInput{
		NodeID: "node-1", CommandID: "cmd-1", Status: domain.CommandStatus("BOGUS"),
	})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
}
