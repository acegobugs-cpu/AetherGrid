package reconcile

import (
	"context"
	"sync"
)

// semaphore is a counting semaphore bounding how many recovery operations run
// concurrently (Phase 9 #34/#35). Starting fifty Terraform applies at once
// would overload the provider, the control plane and the Kubernetes API; this
// protects all of them.
type semaphore struct {
	mu      sync.Mutex
	slots   chan struct{}
	waiters int
}

func newSemaphore(limit int) *semaphore {
	if limit < 1 {
		limit = 1
	}
	return &semaphore{slots: make(chan struct{}, limit)}
}

// acquire takes one slot, blocking while none are free. It returns false when
// ctx is cancelled first.
func (s *semaphore) acquire(ctx context.Context) bool {
	s.mu.Lock()
	s.waiters++
	s.mu.Unlock()

	select {
	case s.slots <- struct{}{}:
		s.mu.Lock()
		s.waiters--
		s.mu.Unlock()
		return true
	case <-ctx.Done():
		s.mu.Lock()
		s.waiters--
		s.mu.Unlock()
		return false
	}
}

// tryAcquire takes a slot only when one is immediately free.
func (s *semaphore) tryAcquire() bool {
	select {
	case s.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

// release returns a slot. Acquired/release must be paired.
func (s *semaphore) release() {
	select {
	case <-s.slots:
	default:
	}
}

// waiting reports how many goroutines are currently blocked on acquire.
func (s *semaphore) waiting() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waiters
}
