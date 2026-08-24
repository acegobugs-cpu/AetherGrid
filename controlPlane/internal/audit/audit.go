// Package audit records security-sensitive events. Audit logging answers a
// different question from application logs: not "what did the software do"
// but "who caused or authorized this action". Events are structured,
// secret-free, and persisted to the audit_events table in addition to being
// emitted on the standard logger.
package audit

import (
	"context"
	"log"
	"sync"
	"time"
)

// Operation names used across the control plane.
const (
	OpAuthenticationFailed    = "AuthenticationFailed"
	OpAuthorizationDenied     = "AuthorizationDenied"
	OpNodeRegistered          = "NodeRegistered"
	OpCredentialIssued        = "CredentialIssued"
	OpCredentialRotated       = "CredentialRotated"
	OpCredentialRevoked       = "CredentialRevoked"
	OpNodeDeleted             = "NodeDeleted"
	OpReconcileTriggered      = "ReconciliationTriggered"
	OpRecoveryReset           = "RecoveryTriggered"
	OpInfrastructureDestroyed = "InfrastructureDestroyed"
	OpInfrastructureCreated   = "InfrastructureProvisioned"
	OpCommandDispatched       = "KubernetesOperation"
)

// Event results.
const (
	ResultSuccess = "success"
	ResultDenied  = "denied"
	ResultFailure = "failure"
)

// Event describes one security-relevant occurrence. It never carries secret
// values; actor identifiers are principal IDs (role names or node IDs), and
// credential references are hashes only when explicitly needed (never by
// default).
type Event struct {
	Time      time.Time
	Operation string
	Actor     string
	ActorType string
	Resource  string
	Result    string
	RequestID string
	Source    string
	Reason    string
}

// Repository persists audit events.
type Repository interface {
	Insert(ctx context.Context, event Event) error
}

// Logger emits audit events to both the application logger (structured
// key=value lines) and the durable repository.
type Logger struct {
	logger *log.Logger
	repo   Repository
	now    func() time.Time

	mu      sync.Mutex
	persist bool
}

// NewLogger constructs an audit logger. A nil repository disables durable
// persistence (events still reach the log writer); this is used by unit
// tests only.
func NewLogger(logger *log.Logger, repo Repository) *Logger {
	return &Logger{
		logger:  logger,
		repo:    repo,
		now:     time.Now().UTC,
		persist: repo != nil,
	}
}

// SetClock overrides the time source (used by tests).
func (l *Logger) SetClock(now func() time.Time) { l.now = now }

// Record writes one audit event. Persistence failures are logged but never
// block request handling; the append-only table is best-effort while the
// stdout line is always emitted.
func (l *Logger) Record(ctx context.Context, event Event) {
	if event.Time.IsZero() {
		event.Time = l.now()
	}
	if event.Result == "" {
		event.Result = ResultSuccess
	}
	if event.Actor == "" {
		event.Actor = "unknown"
	}
	if event.ActorType == "" {
		event.ActorType = "unknown"
	}

	l.logger.Printf("AUDIT operation=%s actor=%s actor_type=%s resource=%q result=%s request_id=%s source=%s reason=%q",
		event.Operation, event.Actor, event.ActorType, event.Resource, event.Result, event.RequestID, event.Source, event.Reason)

	if !l.persist {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.repo.Insert(ctx, event); err != nil {
		l.logger.Printf("AUDIT persistence failed for %s: %v", event.Operation, err)
	}
}
