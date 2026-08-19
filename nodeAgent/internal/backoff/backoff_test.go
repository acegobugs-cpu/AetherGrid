package backoff

import (
	"testing"
	"time"
)

func TestNextDelayGrowsExponentially(t *testing.T) {
	initial := time.Second
	max := 30 * time.Second

	sequence := []time.Duration{
		NextDelay(0, initial, max),
		NextDelay(1, initial, max),
		NextDelay(2, initial, max),
		NextDelay(3, initial, max),
	}
	want := []time.Duration{1, 2, 4, 8}
	for i := range sequence {
		if sequence[i] != want[i]*time.Second {
			t.Errorf("attempt %d: expected %v, got %v", i, want[i]*time.Second, sequence[i])
		}
	}
}

func TestNextDelayCapsAtMaximum(t *testing.T) {
	initial := 100 * time.Millisecond
	max := 500 * time.Millisecond

	for attempt := 0; attempt < 20; attempt++ {
		if got := NextDelay(attempt, initial, max); got > max {
			t.Errorf("attempt %d: delay %v exceeds max %v", attempt, got, max)
		}
	}

	// A large number of attempts must converge to exactly max.
	if got := NextDelay(100, initial, max); got != max {
		t.Errorf("expected delay to cap at %v, got %v", max, got)
	}
}

func TestNextDelayWithMaxEqualToInitial(t *testing.T) {
	initial := 2 * time.Second
	max := 2 * time.Second

	for attempt := 0; attempt < 5; attempt++ {
		if got := NextDelay(attempt, initial, max); got != initial {
			t.Errorf("attempt %d: expected constant delay %v, got %v", attempt, initial, got)
		}
	}
}

func TestNextDelaySanitizesBadInput(t *testing.T) {
	// Zero initial falls back to a sane default.
	if got := NextDelay(0, 0, time.Minute); got != time.Second {
		t.Errorf("expected fallback of 1s, got %v", got)
	}
	// Max below initial is clamped up.
	if got := NextDelay(0, 5*time.Second, time.Second); got != 5*time.Second {
		t.Errorf("expected max clamped to initial, got %v", got)
	}
	// Negative attempts behave like the first attempt.
	if got := NextDelay(-3, time.Second, time.Minute); got != time.Second {
		t.Errorf("expected negative attempt to behave as first, got %v", got)
	}
}
