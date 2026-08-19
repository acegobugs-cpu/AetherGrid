// Package service contains the business logic of the control plane. Services
// depend on the repository abstraction and are independent of both HTTP and
// the concrete database implementation.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
	"github.com/acegobugs-cpu/AetherGrid/internal/repository"

	"github.com/google/uuid"
)

// CommandService manages the command queue used to dispatch instructions to
// edge node agents. It validates commands, tracks their lifecycle and records
// agent-reported results.
type CommandService struct {
	commands repository.CommandRepository
	nodes    repository.NodeRepository
}

// NewCommandService constructs a CommandService backed by the given
// repositories.
func NewCommandService(commands repository.CommandRepository, nodes repository.NodeRepository) *CommandService {
	return &CommandService{commands: commands, nodes: nodes}
}

// CreateCommandInput carries the operator-supplied fields for a new command.
type CreateCommandInput struct {
	NodeID     string
	Type       string
	Parameters json.RawMessage
}

// Create validates the input and persists a new PENDING command for the node.
// It returns repository.ErrNotFound if the node does not exist.
func (s *CommandService) Create(ctx context.Context, input CreateCommandInput) (*domain.Command, error) {
	if strings.TrimSpace(input.Type) == "" {
		return nil, &ValidationError{Message: "command type is required"}
	}

	if _, err := s.nodes.GetByID(ctx, input.NodeID); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	command := &domain.Command{
		ID:         uuid.NewString(),
		NodeID:     input.NodeID,
		Type:       strings.TrimSpace(input.Type),
		Parameters: normalizedJSON(input.Parameters),
		Status:     domain.CommandPending,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := s.commands.Create(ctx, command); err != nil {
		return nil, err
	}
	return command, nil
}

// ListByNode returns the commands for a node, oldest first. When status is
// non-empty it filters to commands in exactly that state.
func (s *CommandService) ListByNode(ctx context.Context, nodeID string, status domain.CommandStatus) ([]*domain.Command, error) {
	if status != "" && !status.Valid() {
		return nil, &ValidationError{Message: fmt.Sprintf("invalid command status %q", status)}
	}

	var commands []*domain.Command
	var err error
	if status == domain.CommandPending {
		commands, err = s.commands.ListPendingByNode(ctx, nodeID)
	} else {
		commands, err = s.commands.ListByNode(ctx, nodeID)
	}
	if err != nil {
		return nil, err
	}

	if status == "" || status == domain.CommandPending {
		return commands, nil
	}

	filtered := commands[:0]
	for _, command := range commands {
		if command.Status == status {
			filtered = append(filtered, command)
		}
	}
	return filtered, nil
}

// ReportResultInput carries an agent-reported outcome for a command.
type ReportResultInput struct {
	NodeID    string
	CommandID string
	Status    domain.CommandStatus
	Result    json.RawMessage
	Error     string
}

// ReportResult records an agent-reported result for a command. The node ID is
// verified against the command's owner to prevent cross-node result spoofing.
// Duplicate reports for an already-terminal command are ignored so a
// redelivered result cannot clobber the original outcome. It returns
// repository.ErrNotFound if the command does not exist or belongs to a
// different node.
func (s *CommandService) ReportResult(ctx context.Context, input ReportResultInput) (*domain.Command, error) {
	if !input.Status.Valid() {
		return nil, &ValidationError{Message: fmt.Sprintf("invalid command status %q", input.Status)}
	}

	command, err := s.commands.GetByID(ctx, input.CommandID)
	if err != nil {
		return nil, err
	}

	if command.NodeID != input.NodeID {
		return nil, repository.ErrNotFound
	}

	// Idempotency: a terminal command is never overwritten.
	if command.Status.Terminal() {
		return command, nil
	}

	command.Status = input.Status
	command.Result = normalizedJSON(input.Result)
	command.Error = strings.TrimSpace(input.Error)
	command.UpdatedAt = time.Now().UTC()

	if err := s.commands.Update(ctx, command); err != nil {
		return nil, err
	}
	return command, nil
}

// IsCommandNotFound reports whether err represents a missing command.
func IsCommandNotFound(err error) bool {
	return errors.Is(err, repository.ErrNotFound)
}

func normalizedJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	return raw
}
