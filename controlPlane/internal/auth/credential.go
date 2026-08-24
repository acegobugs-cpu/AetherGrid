// Package auth implements the Phase 10 machine-identity and credential
// lifecycle for AETHER-GRID: bootstrap credentials for node registration,
// long-lived agent credentials, static human API keys with role assignment,
// rotation and revocation.
//
// Security properties:
//
//   - Credentials are opaque random 256-bit tokens ("agr_..." prefix).
//   - Only the SHA-256 hash of a token is persisted; plaintext is returned
//     exactly once by the issuing call.
//   - Bootstrap credentials are single-use and short-lived, scoped to one
//     node, and carry no privileges beyond exchanging themselves for an
//     agent credential.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Credential kinds.
const (
	KindBootstrap = "bootstrap"
	KindAgent     = "agent"
)

// Credential statuses.
const (
	StatusActive  = "active"
	StatusUsed    = "used"
	StatusRevoked = "revoked"
)

// tokenPrefix marks every AETHER-GRID issued credential so accidental
// leakage is recognizable in triage.
const tokenPrefix = "agr_"

// ErrInvalidCredential is returned when a presented credential does not
// exist. The same error is used for unknown and malformed tokens so callers
// cannot distinguish them (no oracle).
var ErrInvalidCredential = errors.New("invalid credential")

// ErrExpiredCredential is returned when a credential exists but its lifetime
// has ended.
var ErrExpiredCredential = errors.New("credential expired")

// ErrRevokedCredential is returned when a credential has been revoked.
var ErrRevokedCredential = errors.New("credential revoked")

// ErrUsedCredential is returned when a single-use bootstrap credential has
// already been consumed.
var ErrUsedCredential = errors.New("credential already used")

// ErrWrongNode is returned when a valid credential is presented for a node it
// was not issued for. This prevents cross-node identity spoofing.
var ErrWrongNode = errors.New("credential not valid for this node")

// Credential is the persisted representation of one issued credential.
type Credential struct {
	TokenHash  string // SHA-256 hex of the plaintext token
	NodeID     string
	Kind       string // KindBootstrap or KindAgent
	Status     string // StatusActive, StatusUsed or StatusRevoked
	CreatedAt  time.Time
	ExpiresAt  time.Time
	UsedAt     *time.Time
	RevokedAt  *time.Time
	LastUsedAt *time.Time
}

// Active reports whether the credential currently grants access.
func (c *Credential) Active(now time.Time) error {
	switch c.Status {
	case StatusRevoked:
		return ErrRevokedCredential
	case StatusUsed:
		return ErrUsedCredential
	case StatusActive:
	default:
		return ErrInvalidCredential
	}
	if now.After(c.ExpiresAt) {
		return ErrExpiredCredential
	}
	return nil
}

// NewToken generates a fresh random credential token and its SHA-256 hash.
// The plaintext must be shown to the caller exactly once.
func NewToken() (plaintext string, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generating credential randomness: %w", err)
	}
	plaintext = tokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return plaintext, HashToken(plaintext), nil
}

// HashToken derives the persisted form of a token: lowercase hex SHA-256.
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
