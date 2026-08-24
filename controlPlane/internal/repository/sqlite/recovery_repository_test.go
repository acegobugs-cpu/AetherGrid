package sqlite

import (
	"context"
	"testing"
	"time"

	"AetherGrid/controlPlane/internal/domain"
)

// TestRepositoryRecoveryFieldsRoundTrip verifies that the Phase 9 recovery
// metadata survives a write and read cycle (spec #32: recovery state must be
// persisted, never only in process memory).
func TestRepositoryRecoveryFieldsRoundTrip(t *testing.T) {
	repo := newTestRepository(t)
	defer repo.Close()
	ctx := context.Background()

	// Storage precision is microseconds; match it so equality holds.
	lastRecovery := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	nextRetry := time.Now().UTC().Add(30 * time.Minute).Truncate(time.Microsecond)
	node := &domain.Node{
		ID:               "11111111-2222-3333-4444-555555555555",
		Name:             "worker-recovery",
		Status:           domain.StatusOffline,
		DesiredStatus:    domain.StatusReady,
		Role:             domain.RoleWorker,
		RecoveryState:    domain.RecoverySuspected,
		RecoveryFailure:  string(domain.FailureInfrastructure),
		RecoveryAttempts: 2,
		LastRecoveryAt:   &lastRecovery,
		NextRetryAt:      &nextRetry,
		FailureStreak:    1,
	}
	if err := repo.Create(ctx, node); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	got, err := repo.GetByID(ctx, node.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.Role != domain.RoleWorker {
		t.Errorf("role = %q, want Worker", got.Role)
	}
	if got.RecoveryState != domain.RecoverySuspected {
		t.Errorf("recovery_state = %q, want SUSPECTED", got.RecoveryState)
	}
	if got.RecoveryFailure != string(domain.FailureInfrastructure) {
		t.Errorf("recovery_failure = %q, want INFRASTRUCTURE", got.RecoveryFailure)
	}
	if got.RecoveryAttempts != 2 {
		t.Errorf("recovery_attempts = %d, want 2", got.RecoveryAttempts)
	}
	if got.LastRecoveryAt == nil || !got.LastRecoveryAt.Equal(lastRecovery) {
		t.Errorf("last_recovery_at = %v, want %v", got.LastRecoveryAt, lastRecovery)
	}
	if got.NextRetryAt == nil || !got.NextRetryAt.Equal(nextRetry) {
		t.Errorf("next_retry_at = %v, want %v", got.NextRetryAt, nextRetry)
	}
	if got.FailureStreak != 1 {
		t.Errorf("failure_streak = %d, want 1", got.FailureStreak)
	}

	// UpdateReconciliation must persist recovery transitions too.
	got.RecoveryState = domain.RecoveryBlocked
	got.FailureStreak = flappingStreakForTest
	if err := repo.UpdateReconciliation(ctx, got); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	reread, err := repo.GetByID(ctx, node.ID)
	if err != nil {
		t.Fatalf("re-read failed: %v", err)
	}
	if reread.RecoveryState != domain.RecoveryBlocked || reread.FailureStreak != flappingStreakForTest {
		t.Errorf("recovery transition not persisted: state=%s streak=%d",
			reread.RecoveryState, reread.FailureStreak)
	}
}

const flappingStreakForTest = 4
