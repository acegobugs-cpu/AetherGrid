package service

import (
	"context"
	"encoding/json"

	"github.com/acegobugs-cpu/AetherGrid/internal/domain"
	"github.com/acegobugs-cpu/AetherGrid/internal/repository"
)

// mockNodeRepository is an in-memory implementation of repository.NodeRepository
// used by service unit tests.
type mockNodeRepository struct {
	nodes  map[string]*domain.Node
	byName map[string]*domain.Node
}

func newMockNodeRepository() *mockNodeRepository {
	return &mockNodeRepository{
		nodes:  make(map[string]*domain.Node),
		byName: make(map[string]*domain.Node),
	}
}

func (m *mockNodeRepository) Create(_ context.Context, node *domain.Node) error {
	if _, exists := m.byName[node.Name]; exists {
		return repository.ErrConflict
	}
	stored := cloneNode(node)
	m.nodes[node.ID] = stored
	m.byName[node.Name] = stored
	return nil
}

func (m *mockNodeRepository) GetByID(_ context.Context, id string) (*domain.Node, error) {
	node, ok := m.nodes[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return cloneNode(node), nil
}

func (m *mockNodeRepository) GetAll(_ context.Context) ([]*domain.Node, error) {
	nodes := make([]*domain.Node, 0, len(m.nodes))
	for _, node := range m.nodes {
		nodes = append(nodes, cloneNode(node))
	}
	return nodes, nil
}

func (m *mockNodeRepository) Update(_ context.Context, node *domain.Node) error {
	if _, ok := m.nodes[node.ID]; !ok {
		return repository.ErrNotFound
	}
	stored := cloneNode(node)
	m.nodes[node.ID] = stored
	m.byName[node.Name] = stored
	return nil
}

func (m *mockNodeRepository) UpdateReconciliation(_ context.Context, node *domain.Node) error {
	if _, ok := m.nodes[node.ID]; !ok {
		return repository.ErrNotFound
	}
	m.nodes[node.ID] = cloneNode(node)
	return nil
}

func (m *mockNodeRepository) Delete(_ context.Context, id string) error {
	node, ok := m.nodes[id]
	if !ok {
		return repository.ErrNotFound
	}
	delete(m.nodes, id)
	delete(m.byName, node.Name)
	return nil
}

func cloneNode(node *domain.Node) *domain.Node {
	copied := *node
	if node.LastHeartbeat != nil {
		heartbeat := *node.LastHeartbeat
		copied.LastHeartbeat = &heartbeat
	}
	return &copied
}

// mockCommandRepository is an in-memory implementation of
// repository.CommandRepository used by service unit tests.
type mockCommandRepository struct {
	commands map[string]*domain.Command
}

func newMockCommandRepository() *mockCommandRepository {
	return &mockCommandRepository{commands: make(map[string]*domain.Command)}
}

func (m *mockCommandRepository) Create(_ context.Context, command *domain.Command) error {
	stored := cloneCommand(command)
	m.commands[command.ID] = stored
	return nil
}

func (m *mockCommandRepository) GetByID(_ context.Context, id string) (*domain.Command, error) {
	command, ok := m.commands[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return cloneCommand(command), nil
}

func (m *mockCommandRepository) ListByNode(_ context.Context, nodeID string) ([]*domain.Command, error) {
	var commands []*domain.Command
	for _, command := range m.commands {
		if command.NodeID == nodeID {
			commands = append(commands, cloneCommand(command))
		}
	}
	return commands, nil
}

func (m *mockCommandRepository) ListPendingByNode(_ context.Context, nodeID string) ([]*domain.Command, error) {
	commands, err := m.ListByNode(context.Background(), nodeID)
	if err != nil {
		return nil, err
	}
	pending := commands[:0]
	for _, command := range commands {
		if !command.Status.Terminal() {
			pending = append(pending, command)
		}
	}
	return pending, nil
}

func (m *mockCommandRepository) Update(_ context.Context, command *domain.Command) error {
	if _, ok := m.commands[command.ID]; !ok {
		return repository.ErrNotFound
	}
	m.commands[command.ID] = cloneCommand(command)
	return nil
}

func cloneCommand(command *domain.Command) *domain.Command {
	copied := *command
	if command.Parameters != nil {
		copied.Parameters = append(json.RawMessage(nil), command.Parameters...)
	}
	if command.Result != nil {
		copied.Result = append(json.RawMessage(nil), command.Result...)
	}
	return &copied
}
