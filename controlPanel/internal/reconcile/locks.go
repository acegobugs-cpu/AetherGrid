package reconcile

import (
	"sync"
)

// nodeLocks serializes reconciliation per node so two workers can never
// reconcile the same node concurrently. Locks are reference counted: the
// entry is released once the last holder leaves, so a large cluster does not
// leak one entry per node.
type nodeLocks struct {
	mu    sync.Mutex
	locks map[string]*refLock
}

type refLock struct {
	mu   sync.Mutex
	refs int
}

func newNodeLocks() *nodeLocks {
	return &nodeLocks{locks: make(map[string]*refLock)}
}

// acquire takes the per-node lock, creating the entry on first use.
func (l *nodeLocks) acquire(nodeID string) {
	l.mu.Lock()
	lock := l.locks[nodeID]
	if lock == nil {
		lock = &refLock{}
		l.locks[nodeID] = lock
	}
	lock.refs++
	l.mu.Unlock()

	lock.mu.Lock()
}

// release unlocks the per-node lock and drops the entry when the last holder
// leaves.
func (l *nodeLocks) release(nodeID string) {
	l.mu.Lock()
	lock := l.locks[nodeID]
	if lock != nil {
		lock.refs--
		if lock.refs == 0 {
			delete(l.locks, nodeID)
		}
	}
	l.mu.Unlock()

	if lock != nil {
		lock.mu.Unlock()
	}
}
