package reconcile

import (
	"math"
	"math/rand"
	"time"
)

// backoff returns the delay before the nth retry attempt, growing
// exponentially from base up to max. attempt is 1-based.
func backoff(attempt int, base, max time.Duration) time.Duration {
	if attempt <= 1 {
		return base
	}
	// Cap the exponent to avoid absurd shifts on very high attempt counts.
	exponent := math.Min(float64(attempt-1), 16)
	delay := time.Duration(float64(base) * math.Pow(2, exponent))
	if delay > max {
		return max
	}
	return delay
}

// backoffJittered applies full jitter within [delay/2, delay] so concurrent
// failures do not synchronize their retries (Phase 9 #31). Jitter never
// increases the computed delay.
func backoffJittered(delay time.Duration, rng *rand.Rand) time.Duration {
	if delay <= 0 {
		return delay
	}
	half := delay / 2
	if half <= 0 {
		return delay
	}
	return half + time.Duration(rng.Int63n(int64(half)+1))
}

// nextAttempt computes the 1-based attempt number following a failed cycle.
func nextAttempt(previous int) int {
	return previous + 1
}
