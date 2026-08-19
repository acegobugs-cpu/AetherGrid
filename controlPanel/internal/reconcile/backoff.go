package reconcile

import (
	"math"
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

// nextAttempt computes the 1-based attempt number following a failed cycle.
func nextAttempt(previous int) int {
	return previous + 1
}
