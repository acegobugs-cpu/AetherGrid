// Package backoff provides the exponential backoff calculation used when the
// agent retries failed control-plane communication.
package backoff

import "time"

// NextDelay returns the delay to wait before retry number `attempt` (0-based)
// given the initial delay and a maximum cap. Delays grow exponentially:
// initial, 2*initial, 4*initial, ... up to max.
func NextDelay(attempt int, initial, max time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if initial <= 0 {
		initial = time.Second
	}
	if max < initial {
		max = initial
	}

	delay := initial
	for i := 0; i < attempt; i++ {
		if delay >= max {
			return max
		}
		if delay > max/2 {
			return max
		}
		delay *= 2
	}
	return delay
}
