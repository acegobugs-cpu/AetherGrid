package middleware

import (
	"net/http"
	"sync"
	"time"
)

// RateLimiter is a fixed-window per-key limiter protecting sensitive
// endpoints (registration, reconciliation, recovery, provisioning) from
// abuse and denial-of-wallet style repetition. It is in-memory by design:
// limits are per control-plane instance, which is sufficient for the
// single-node control plane architecture.
type RateLimiter struct {
	mu     sync.Mutex
	window time.Duration
	limit  int
	now    func() time.Time

	buckets map[string]*rateBucket
}

type rateBucket struct {
	windowStart time.Time
	count       int
}

// NewRateLimiter allows `limit` requests per key per `window`. Non-positive
// values fall back to sane defaults.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	if limit <= 0 {
		limit = 60
	}
	if window <= 0 {
		window = time.Minute
	}
	return &RateLimiter{
		window:  window,
		limit:   limit,
		now:     timeNow,
		buckets: make(map[string]*rateBucket),
	}
}

// Allow reports whether one more request from key is permitted this window.
func (l *RateLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	bucket, exists := l.buckets[key]
	if !exists || now.Sub(bucket.windowStart) >= l.window {
		if len(l.buckets) > 4096 {
			l.sweepLocked(now)
		}
		l.buckets[key] = &rateBucket{windowStart: now, count: 1}
		return true
	}
	if bucket.count >= l.limit {
		return false
	}
	bucket.count++
	return true
}

// sweepLocked evicts buckets whose windows have fully elapsed. Called only
// when the map grows large, keeping steady-state overhead negligible.
func (l *RateLimiter) sweepLocked(now time.Time) {
	for key, bucket := range l.buckets {
		if now.Sub(bucket.windowStart) >= l.window {
			delete(l.buckets, key)
		}
	}
}

// RateLimited wraps a handler with a rate limit keyed by client identity.
// Rejected requests receive 429 with a Retry-After hint.
func RateLimited(limiter *RateLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Allow(ClientBucket(r)) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limit exceeded"}`))
			return
		}
		next(w, r)
	}
}
