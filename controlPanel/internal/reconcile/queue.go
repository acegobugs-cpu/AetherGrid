package reconcile

import (
	"sync"
)

// workQueue is a concurrent, deduplicating queue of node IDs awaiting
// reconciliation. A node can be enqueued many times but is only handed to a
// worker once until it is processed.
type workQueue struct {
	mu      sync.Mutex
	notify  chan struct{}
	pending map[string]struct{}
	order   []string
}

func newWorkQueue() *workQueue {
	return &workQueue{
		notify:  make(chan struct{}, 1),
		pending: make(map[string]struct{}),
	}
}

// Enqueue marks a node as needing reconciliation. It is safe for concurrent
// use and never blocks.
func (q *workQueue) Enqueue(nodeID string) {
	q.mu.Lock()
	if _, exists := q.pending[nodeID]; !exists {
		q.pending[nodeID] = struct{}{}
		q.order = append(q.order, nodeID)
	}
	q.mu.Unlock()

	select {
	case q.notify <- struct{}{}:
	default:
	}
}

// Dequeue removes and returns the next node ID, returning "" when the queue
// is empty.
func (q *workQueue) Dequeue() string {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.order) == 0 {
		return ""
	}
	nodeID := q.order[0]
	q.order = q.order[1:]
	delete(q.pending, nodeID)
	return nodeID
}

// Len reports how many nodes are pending.
func (q *workQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.order)
}
