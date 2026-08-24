package auth

import (
	"context"
	"time"
)

// Repository is the persistence interface for the credential store.
// Implementations must only ever persist token hashes, never plaintexts.
type Repository interface {
	// Create persists a new credential. It must reject duplicate token
	// hashes with ErrTokenExists (statistically impossible for fresh random
	// tokens, but enforced for defense in depth).
	Create(ctx context.Context, credential *Credential) error
	// GetByTokenHash returns the credential with the given hash or
	// ErrNotFound when unknown.
	GetByTokenHash(ctx context.Context, tokenHash string) (*Credential, error)
	// UpdateStatus transitions a credential to a new status and records the
	// transition time. It returns ErrNotFound when the hash is unknown.
	UpdateStatus(ctx context.Context, tokenHash, status string, at time.Time) error
	// TouchLastUsed records successful use of a credential. Failures are
	// non-fatal for authentication.
	TouchLastUsed(ctx context.Context, tokenHash string, at time.Time) error
	// ListByNode returns every credential issued for a node.
	ListByNode(ctx context.Context, nodeID string) ([]*Credential, error)
}

// Sentinel errors matching the repository package conventions.
var (
	ErrNotFound    = errString("credential not found")
	ErrTokenExists = errString("credential already exists")
)

type errString string

func (e errString) Error() string { return string(e) }
