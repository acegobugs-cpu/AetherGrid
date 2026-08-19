package service

import (
	"context"

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
