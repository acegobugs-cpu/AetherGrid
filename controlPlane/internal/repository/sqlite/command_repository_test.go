package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"AetherGrid/controlPlane/internal/domain"
	"AetherGrid/controlPlane/internal/repository"

	"github.com/google/uuid"
)

func newTestCommandRepository(t *testing.T) (*CommandRepository, *NodeRepository) {
	t.Helper()
	nodeRepo := newTestRepository(t)
	return NewCommandRepository(nodeRepo.DB()), nodeRepo
}

func sampleCommand(nodeID string) *domain.Command {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return &domain.Command{
		ID:         uuid.NewString(),
		NodeID:     nodeID,
		Type:       "GET_STATUS",
		Parameters: json.RawMessage(`{"detail":"full"}`),
		Status:     domain.CommandPending,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func TestCommandRepositoryCreateAndGet(t *testing.T) {
	commands, nodes := newTestCommandRepository(t)
	ctx := context.Background()

	node := sampleNode(t)
	if err := nodes.Create(ctx, node); err != nil {
		t.Fatalf("creating node: %v", err)
	}

	command := sampleCommand(node.ID)
	if err := commands.Create(ctx, command); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	got, err := commands.GetByID(ctx, command.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.ID != command.ID || got.NodeID != command.NodeID || got.Type != command.Type {
		t.Errorf("unexpected command: %+v", got)
	}
	if got.Status != domain.CommandPending {
		t.Errorf("expected PENDING, got %q", got.Status)
	}
	if string(got.Parameters) != `{"detail":"full"}` {
		t.Errorf("expected parameters preserved, got %q", got.Parameters)
	}
}

func TestCommandRepositoryGetMissing(t *testing.T) {
	commands, _ := newTestCommandRepository(t)

	if _, err := commands.GetByID(context.Background(), "missing"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestCommandRepositoryListByNode(t *testing.T) {
	commands, nodes := newTestCommandRepository(t)
	ctx := context.Background()

	node := sampleNode(t)
	if err := nodes.Create(ctx, node); err != nil {
		t.Fatalf("creating node: %v", err)
	}

	for i := 0; i < 3; i++ {
		command := sampleCommand(node.ID)
		if err := commands.Create(ctx, command); err != nil {
			t.Fatalf("create %d failed: %v", i, err)
		}
	}

	all, err := commands.ListByNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(all))
	}
}

func TestCommandRepositoryListPendingByNode(t *testing.T) {
	commands, nodes := newTestCommandRepository(t)
	ctx := context.Background()

	node := sampleNode(t)
	if err := nodes.Create(ctx, node); err != nil {
		t.Fatalf("creating node: %v", err)
	}

	pending := sampleCommand(node.ID)
	if err := commands.Create(ctx, pending); err != nil {
		t.Fatalf("create pending failed: %v", err)
	}
	done := sampleCommand(node.ID)
	done.Status = domain.CommandSucceeded
	done.Result = json.RawMessage(`{"status":"READY"}`)
	if err := commands.Create(ctx, done); err != nil {
		t.Fatalf("create done failed: %v", err)
	}

	got, err := commands.ListPendingByNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("list pending failed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 pending command, got %d", len(got))
	}
	if got[0].ID != pending.ID {
		t.Errorf("expected pending command %s, got %s", pending.ID, got[0].ID)
	}
}

func TestCommandRepositoryUpdate(t *testing.T) {
	commands, nodes := newTestCommandRepository(t)
	ctx := context.Background()

	node := sampleNode(t)
	if err := nodes.Create(ctx, node); err != nil {
		t.Fatalf("creating node: %v", err)
	}

	command := sampleCommand(node.ID)
	if err := commands.Create(ctx, command); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	command.Status = domain.CommandSucceeded
	command.Result = json.RawMessage(`{"hostname":"edge-01"}`)
	command.Error = ""
	command.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)

	if err := commands.Update(ctx, command); err != nil {
		t.Fatalf("update failed: %v", err)
	}

	got, err := commands.GetByID(ctx, command.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.Status != domain.CommandSucceeded {
		t.Errorf("expected SUCCEEDED, got %q", got.Status)
	}
	if string(got.Result) != `{"hostname":"edge-01"}` {
		t.Errorf("expected result preserved, got %q", got.Result)
	}
}

func TestCommandRepositoryUpdateMissing(t *testing.T) {
	commands, _ := newTestCommandRepository(t)

	command := sampleCommand("node-1")
	command.ID = "missing"
	if err := commands.Update(context.Background(), command); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
