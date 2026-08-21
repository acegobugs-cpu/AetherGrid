package reconcile

import (
	"context"
	"time"

	"AetherGrid/controlPlane/internal/domain"
)

// Strategy is the observer's staleness policy for a single node.
type Strategy struct {
	// NodeID identifies the node.
	NodeID string
	// Desired is the state the operator wants the node to converge to.
	Desired domain.DesiredState
	// Actual is the last observed state of the node.
	Actual domain.ActualState
	// Stale reports whether the node's heartbeat is older than the configured
	// timeout. Nodes that have never sent a heartbeat use their stored status.
	Stale bool
}

// StateObserver is the observer abstraction. It reads node state and reports,
// for every node, whether the actual state is stale.
type StateObserver interface {
	// Observe returns the reconciliation strategies for every known node.
	Observe(ctx context.Context) ([]Strategy, error)
	// Node returns the strategy for a single node.
	Node(ctx context.Context, id string) (Strategy, error)
}

// RepositoryObserver reads node state from the NodeRepository and flags nodes
// whose heartbeat has gone stale as OFFLINE, so the planner can schedule
// recovery.
type RepositoryObserver struct {
	repo    NodeRepository
	timeout time.Duration
	now     func() time.Time
}

// NewRepositoryObserver constructs an observer over the given repository. now
// is injectable for tests; pass nil to use the system clock.
func NewRepositoryObserver(repo NodeRepository, timeout time.Duration, now func() time.Time) *RepositoryObserver {
	if now == nil {
		now = time.Now
	}
	return &RepositoryObserver{repo: repo, timeout: timeout, now: now}
}

// Observe returns a strategy per node, ordered by the repository.
func (o *RepositoryObserver) Observe(ctx context.Context) ([]Strategy, error) {
	nodes, err := o.repo.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	strategies := make([]Strategy, 0, len(nodes))
	for _, node := range nodes {
		strategies = append(strategies, o.strategy(node))
	}
	return strategies, nil
}

// Node returns the strategy for a single node.
func (o *RepositoryObserver) Node(ctx context.Context, id string) (Strategy, error) {
	node, err := o.repo.GetByID(ctx, id)
	if err != nil {
		return Strategy{}, err
	}
	return o.strategy(node), nil
}

func (o *RepositoryObserver) strategy(node *domain.Node) Strategy {
	strategy := Strategy{
		NodeID:  node.ID,
		Desired: node.DesiredState(),
		Actual:  node.ActualState(),
	}

	// Nodes that have never reported a heartbeat keep their stored status: the
	// heartbeat field is the liveness signal, not the status.
	if node.LastHeartbeat != nil {
		strategy.Stale = o.now().Sub(*node.LastHeartbeat) > o.timeout
	}
	return strategy
}
